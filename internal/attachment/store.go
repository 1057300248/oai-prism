// Package attachment stores caller-owned files, never filesystem paths supplied
// by a caller. The API layer validates file formats before persistence.
package attachment

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const MaxFileBytes = 8 << 20
const maxEntries = 1024

var ErrNotFound = errors.New("attachment not found")
var ErrQuota = errors.New("attachment storage quota exceeded")
var ErrClosed = errors.New("attachment store closed")

type Options struct {
	Enabled    bool          `yaml:"enabled"`
	Path       string        `yaml:"path"`
	KeyEnv     string        `yaml:"key_env"`
	TTL        time.Duration `yaml:"ttl"`
	OwnerBytes int64         `yaml:"owner_bytes"`
	TotalBytes int64         `yaml:"total_bytes"`
	OwnerFiles int           `yaml:"owner_files"`
}

func (o Options) defaults() Options {
	if o.TTL == 0 {
		o.TTL = 24 * time.Hour
	}
	if o.OwnerBytes == 0 {
		o.OwnerBytes = 32 << 20
	}
	if o.TotalBytes == 0 {
		o.TotalBytes = 128 << 20
	}
	if o.OwnerFiles == 0 {
		o.OwnerFiles = 64
	}
	return o
}
func (o Options) Validate() error {
	o = o.defaults()
	if o.TTL < time.Hour || o.TTL > 30*24*time.Hour {
		return errors.New("files.ttl must be 1h..720h")
	}
	if o.OwnerFiles < 1 || o.OwnerFiles > maxEntries || o.OwnerBytes < 1 || o.OwnerBytes > 1<<30 || o.TotalBytes < o.OwnerBytes || o.TotalBytes > 1<<30 {
		return errors.New("invalid bounded file quotas")
	}
	if o.Path != "" && o.KeyEnv == "" {
		return errors.New("persistent files require an encryption key environment variable")
	}
	return nil
}

type File struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int    `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
	Status    string `json:"status"`
	MIME      string `json:"-"`
	Data      []byte `json:"-"`
}
type entry struct {
	File  File
	Owner string
}
type storedPayload struct {
	Filename string
	Purpose  string
	MIME     string
	Data     []byte
}
type Store struct {
	mu        sync.Mutex
	options   Options
	entries   map[string]entry
	db        *sql.DB
	aead      cipher.AEAD
	now       func() time.Time
	stop      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	closed    bool
	closeErr  error
}

func New(o Options) (*Store, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	o = o.defaults()
	s := &Store{options: o, entries: map[string]entry{}, now: time.Now, stop: make(chan struct{})}
	if o.Path != "" {
		key, err := base64.StdEncoding.Strict().DecodeString(os.Getenv(o.KeyEnv))
		if err != nil || len(key) != 32 {
			return nil, errors.New("file encryption key must be base64 of exactly 32 bytes")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		s.aead, err = cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		path, err := filepath.Abs(o.Path)
		if err != nil {
			return nil, err
		}
		if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			return nil, errors.New("file store must be a regular non-symlink database")
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		err = f.Chmod(0600)
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		s.db, err = sql.Open("sqlite", path)
		if err != nil {
			return nil, err
		}
		s.db.SetMaxOpenConns(1)
		for _, stmt := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA journal_size_limit=4194304", "CREATE TABLE IF NOT EXISTS attachments (id TEXT PRIMARY KEY, owner TEXT NOT NULL, created INTEGER NOT NULL, expires INTEGER NOT NULL, size INTEGER NOT NULL, payload BLOB NOT NULL)", "CREATE INDEX IF NOT EXISTS attachments_owner ON attachments(owner,expires)"} {
			if _, err = s.db.Exec(stmt); err != nil {
				_ = s.db.Close()
				return nil, err
			}
		}
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = s.Cleanup(ctx)
				cancel()
			}
		}
	}()
	return s, nil
}
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		s.wg.Wait()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closed = true
		clear(s.entries)
		if s.db != nil {
			s.closeErr = s.db.Close()
		}
	})
	return s.closeErr
}
func (s *Store) cleanupLocked(ctx context.Context) error {
	if s.db != nil {
		_, err := s.db.ExecContext(ctx, "DELETE FROM attachments WHERE expires<=?", s.now().Unix())
		return err
	}
	for id, e := range s.entries {
		if e.File.ExpiresAt <= s.now().Unix() {
			delete(s.entries, id)
		}
	}
	return nil
}
func (s *Store) Cleanup(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return s.cleanupLocked(ctx)
}
func (s *Store) Put(ctx context.Context, owner, name, purpose, mime string, data []byte, ttl time.Duration) (File, error) {
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	if owner == "" || len(data) == 0 || len(data) > MaxFileBytes {
		return File{}, errors.New("invalid attachment")
	}
	if ttl == 0 {
		ttl = s.options.TTL
	}
	if ttl < time.Hour || ttl > s.options.TTL {
		return File{}, errors.New("requested file lifetime exceeds operator retention policy")
	}
	now := s.now().Unix()
	f := File{ID: "file-" + uuid.NewString(), Object: "file", Bytes: len(data), CreatedAt: now, ExpiresAt: now + int64(ttl.Seconds()), Filename: name, Purpose: purpose, Status: "processed", MIME: mime}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return File{}, ErrClosed
	}
	if s.db == nil {
		if err := s.cleanupLocked(ctx); err != nil {
			return File{}, err
		}
		var count int
		var ownerBytes, total int64
		for _, e := range s.entries {
			total += int64(e.File.Bytes)
			if e.Owner == owner {
				count++
				ownerBytes += int64(e.File.Bytes)
			}
		}
		if len(s.entries) >= maxEntries || count >= s.options.OwnerFiles || ownerBytes+int64(len(data)) > s.options.OwnerBytes || total+int64(len(data)) > s.options.TotalBytes {
			return File{}, ErrQuota
		}
		saved := f
		saved.Data = append([]byte(nil), data...)
		s.entries[f.ID] = entry{File: saved, Owner: owner}
		return f, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM attachments WHERE expires<=?", now); err != nil {
		return File{}, err
	}
	var count, all int
	var owned, total int64
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(size),0) FROM attachments WHERE owner=?", owner).Scan(&count, &owned); err != nil {
		return File{}, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(size),0) FROM attachments").Scan(&all, &total); err != nil {
		return File{}, err
	}
	if all >= maxEntries || count >= s.options.OwnerFiles || owned+int64(len(data)) > s.options.OwnerBytes || total+int64(len(data)) > s.options.TotalBytes {
		return File{}, ErrQuota
	}
	body, err := json.Marshal(storedPayload{name, purpose, mime, data})
	if err != nil {
		return File{}, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return File{}, err
	}
	sealed := s.aead.Seal(nonce, nonce, body, aad(f, owner))
	if _, err = tx.ExecContext(ctx, "INSERT INTO attachments(id,owner,created,expires,size,payload) VALUES(?,?,?,?,?,?)", f.ID, owner, f.CreatedAt, f.ExpiresAt, f.Bytes, sealed); err != nil {
		return File{}, err
	}
	if err = tx.Commit(); err != nil {
		return File{}, err
	}
	return f, nil
}
func aad(f File, owner string) []byte {
	return []byte(fmt.Sprintf("files-v1\x00%s\x00%s\x00%d\x00%d\x00%d", f.ID, owner, f.CreatedAt, f.ExpiresAt, f.Bytes))
}
func (s *Store) getLocked(ctx context.Context, id, owner string) (File, error) {
	if s.db == nil {
		e, ok := s.entries[id]
		if !ok || e.Owner != owner || e.File.ExpiresAt <= s.now().Unix() {
			return File{}, ErrNotFound
		}
		f := e.File
		f.Data = append([]byte(nil), f.Data...)
		return f, nil
	}
	var f File
	var sealed []byte
	err := s.db.QueryRowContext(ctx, "SELECT id,created,expires,size,payload FROM attachments WHERE id=? AND owner=? AND expires>?", id, owner, s.now().Unix()).Scan(&f.ID, &f.CreatedAt, &f.ExpiresAt, &f.Bytes, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, err
	}
	n := s.aead.NonceSize()
	if len(sealed) < n || len(sealed) > 2*MaxFileBytes || f.Bytes < 1 || f.Bytes > MaxFileBytes {
		return File{}, errors.New("invalid encrypted file")
	}
	body, err := s.aead.Open(nil, sealed[:n], sealed[n:], aad(f, owner))
	if err != nil {
		return File{}, errors.New("attachment integrity or encryption-key error")
	}
	var p storedPayload
	if json.Unmarshal(body, &p) != nil || len(p.Data) != f.Bytes {
		return File{}, errors.New("invalid attachment payload")
	}
	f.Object = "file"
	f.Status = "processed"
	f.Filename = p.Filename
	f.Purpose = p.Purpose
	f.MIME = p.MIME
	f.Data = p.Data
	return f, nil
}
func (s *Store) Get(ctx context.Context, id, owner string) (File, error) {
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return File{}, ErrClosed
	}
	return s.getLocked(ctx, id, owner)
}
func (s *Store) Delete(ctx context.Context, id, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.db == nil {
		e, ok := s.entries[id]
		if !ok || e.Owner != owner || e.File.ExpiresAt <= s.now().Unix() {
			return ErrNotFound
		}
		delete(s.entries, id)
		return nil
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM attachments WHERE id=? AND owner=? AND expires>?", id, owner, s.now().Unix())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
func (s *Store) List(ctx context.Context, owner string) ([]File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	ids := []string{}
	if s.db == nil {
		for id, e := range s.entries {
			if e.Owner == owner && e.File.ExpiresAt > s.now().Unix() {
				ids = append(ids, id)
			}
		}
	} else {
		rows, err := s.db.QueryContext(ctx, "SELECT id FROM attachments WHERE owner=? AND expires>? ORDER BY created,id LIMIT ?", owner, s.now().Unix(), maxEntries+1)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(ids) > maxEntries {
		return nil, ErrQuota
	}
	out := make([]File, 0, len(ids))
	for _, id := range ids {
		f, err := s.getLocked(ctx, id, owner)
		if err != nil {
			return nil, err
		}
		f.Data = nil
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out, nil
}
