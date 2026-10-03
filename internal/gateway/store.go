package gateway

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const storeTTL = 30 * time.Minute
const storeMaxEntries = 512
const storeMaxBytes = 64 << 20
const storeEntryLimit = 20 << 20

type Snapshot struct {
	Response json.RawMessage `json:"response"`
	Items    []Item          `json:"items"`
}
type storedEntry struct {
	owner   string
	data    []byte
	expires time.Time
}
type responseStore struct {
	mu     sync.Mutex
	memory map[string]storedEntry
	bytes  int
	db     *sql.DB
	aead   cipher.AEAD
	now    func() time.Time
}

func newStore(o Options) (*responseStore, error) {
	s := &responseStore{memory: map[string]storedEntry{}, now: time.Now}
	if o.StorePath == "" {
		return s, nil
	}
	key, err := base64.StdEncoding.Strict().DecodeString(os.Getenv(o.StoreKeyEnv))
	if err != nil || len(key) != 32 {
		return nil, errors.New("store encryption key must contain exactly 32 base64-encoded bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	s.aead, err = cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	path, err := filepath.Abs(o.StorePath)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	s.db, err = sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s.db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "CREATE TABLE IF NOT EXISTS gateway_responses (id TEXT PRIMARY KEY, owner TEXT NOT NULL, expires INTEGER NOT NULL, payload BLOB NOT NULL)", "CREATE INDEX IF NOT EXISTS gateway_responses_expiry ON gateway_responses(expires)"} {
		if _, err = s.db.Exec(statement); err != nil {
			_ = s.db.Close()
			return nil, err
		}
	}
	return s, nil
}
func (s *responseStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.memory)
	s.bytes = 0
	return nil
}
func (s *responseStore) remove(id string) {
	if e, ok := s.memory[id]; ok {
		s.bytes -= len(e.data)
		delete(s.memory, id)
	}
}
func (s *responseStore) cleanup(now time.Time) {
	for id, e := range s.memory {
		if !now.Before(e.expires) {
			s.remove(id)
		}
	}
}
func (s *responseStore) Put(ctx context.Context, id, owner string, snapshot Snapshot) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(raw) > storeEntryLimit {
		return errors.New("response snapshot exceeds storage limit")
	}
	now := s.now()
	expiry := now.Add(storeTTL)
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cleanup(now)
		s.remove(id)
		for len(s.memory) >= storeMaxEntries || s.bytes+len(raw) > storeMaxBytes {
			oldest := ""
			var t time.Time
			for k, v := range s.memory {
				if oldest == "" || v.expires.Before(t) {
					oldest, t = k, v.expires
				}
			}
			if oldest == "" {
				return errors.New("response store capacity exhausted")
			}
			s.remove(oldest)
		}
		s.memory[id] = storedEntry{owner: owner, data: raw, expires: expiry}
		s.bytes += len(raw)
		return nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := s.aead.Seal(nonce, nonce, raw, []byte(id+"\x00"+owner))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM gateway_responses WHERE expires<=?", now.UnixNano()); err != nil {
		return err
	}
	for {
		var count, size int64
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(length(payload)),0) FROM gateway_responses").Scan(&count, &size); err != nil {
			return err
		}
		if count < storeMaxEntries && size+int64(len(sealed)) <= storeMaxBytes {
			break
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM gateway_responses WHERE id=(SELECT id FROM gateway_responses ORDER BY expires,id LIMIT 1)"); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO gateway_responses(id,owner,expires,payload) VALUES(?,?,?,?)", id, owner, expiry.UnixNano(), sealed); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *responseStore) Get(ctx context.Context, id, owner string) (Snapshot, bool, error) {
	var raw []byte
	if s.db == nil {
		s.mu.Lock()
		s.cleanup(s.now())
		e, ok := s.memory[id]
		if ok && e.owner == owner {
			raw = append([]byte(nil), e.data...)
		}
		s.mu.Unlock()
		if raw == nil {
			return Snapshot{}, false, nil
		}
	} else {
		var sealed []byte
		err := s.db.QueryRowContext(ctx, "SELECT payload FROM gateway_responses WHERE id=? AND owner=? AND expires>?", id, owner, s.now().UnixNano()).Scan(&sealed)
		if errors.Is(err, sql.ErrNoRows) {
			return Snapshot{}, false, nil
		}
		if err != nil {
			return Snapshot{}, false, err
		}
		n := s.aead.NonceSize()
		if len(sealed) < n || len(sealed) > storeEntryLimit+1024 {
			return Snapshot{}, false, errors.New("invalid encrypted response snapshot")
		}
		raw, err = s.aead.Open(nil, sealed[:n], sealed[n:], []byte(id+"\x00"+owner))
		if err != nil {
			return Snapshot{}, false, errors.New("response store key mismatch or corrupted snapshot")
		}
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Snapshot{}, false, err
	}
	return snapshot, true, nil
}
func (s *responseStore) Delete(ctx context.Context, id, owner string) (bool, error) {
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cleanup(s.now())
		e, ok := s.memory[id]
		if !ok || e.owner != owner {
			return false, nil
		}
		s.remove(id)
		return true, nil
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM gateway_responses WHERE id=? AND owner=? AND expires>?", id, owner, s.now().UnixNano())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}
