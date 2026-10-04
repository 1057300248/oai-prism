package attachment

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T, persist bool, o Options) *Store {
	t.Helper()
	if persist {
		o.Path = filepath.Join(t.TempDir(), "files.sqlite")
		o.KeyEnv = "FILES_TEST_KEY"
		t.Setenv(o.KeyEnv, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func TestFileOwnershipCopiesAndDeletion(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		s := testStore(t, persistent, Options{})
		ctx := context.Background()
		data := []byte("private data")
		f, err := s.Put(ctx, "a", "notes.txt", "user_data", "text/plain", data, 0)
		if err != nil {
			t.Fatal(err)
		}
		data[0] = 'x'
		got, err := s.Get(ctx, f.ID, "a")
		if err != nil || string(got.Data) != "private data" {
			t.Fatal(got, err)
		}
		got.Data[0] = 'x'
		again, _ := s.Get(ctx, f.ID, "a")
		if string(again.Data) != "private data" {
			t.Fatal("mutable reference")
		}
		if _, err = s.Get(ctx, f.ID, "b"); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-tenant read", err)
		}
		if err = s.Delete(ctx, f.ID, "b"); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-tenant delete", err)
		}
		list, err := s.List(ctx, "b")
		if err != nil || len(list) != 0 {
			t.Fatal("cross-tenant list")
		}
		if err = s.Delete(ctx, f.ID, "a"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Get(ctx, f.ID, "a"); !errors.Is(err, ErrNotFound) {
			t.Fatal("deleted bytes accessible")
		}
	}
}
func TestFileQuotasDoNotEvictAnotherOwner(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		s := testStore(t, persistent, Options{OwnerFiles: 1, OwnerBytes: 8, TotalBytes: 10})
		ctx := context.Background()
		a, err := s.Put(ctx, "a", "a.txt", "user_data", "text/plain", []byte("123456"), 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Put(ctx, "a", "b.txt", "user_data", "text/plain", []byte("1"), 0); !errors.Is(err, ErrQuota) {
			t.Fatal(err)
		}
		if _, err = s.Put(ctx, "b", "b.txt", "user_data", "text/plain", []byte("12345"), 0); !errors.Is(err, ErrQuota) {
			t.Fatal(err)
		}
		if _, err = s.Get(ctx, a.ID, "a"); err != nil {
			t.Fatal("quota evicted existing file")
		}
	}
}
func TestFileExpiryAndCleanup(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		s := testStore(t, persistent, Options{TTL: time.Hour})
		ctx := context.Background()
		f, err := s.Put(ctx, "a", "note.txt", "user_data", "text/plain", []byte("private"), 0)
		if err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		s.now = func() time.Time { return time.Unix(f.ExpiresAt, 0) }
		s.mu.Unlock()
		if _, err = s.Get(ctx, f.ID, "a"); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if err = s.Cleanup(ctx); err != nil {
			t.Fatal(err)
		}
		files, err := s.List(ctx, "a")
		if err != nil || len(files) != 0 {
			t.Fatal("expiry retained live files")
		}
	}
}
func TestEncryptedFilesRestartAndWrongKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "files.sqlite")
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	t.Setenv("FILES_RESTART_KEY", key)
	o := Options{Path: path, KeyEnv: "FILES_RESTART_KEY"}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	secret := "secret-file-text-5487"
	f, err := s.Put(context.Background(), "tenant", "secret-name.txt", "user_data", "text/plain", []byte(secret), 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte("secret-name.txt")) {
		t.Fatal("plaintext at rest")
	}
	s, err = New(o)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), f.ID, "tenant")
	if err != nil || string(got.Data) != secret {
		t.Fatal("restart", err)
	}
	_ = s.Close()
	t.Setenv(o.KeyEnv, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	s, err = New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Get(context.Background(), f.ID, "tenant"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatal("wrong key did not fail integrity")
	}
}
func TestConcurrentQuotaAndClosedStore(t *testing.T) {
	s := testStore(t, false, Options{OwnerFiles: 8})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Put(context.Background(), "a", "x.txt", "user_data", "text/plain", []byte("x"), 0)
			if err != nil && !errors.Is(err, ErrQuota) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	files, err := s.List(context.Background(), "a")
	if err != nil || len(files) != 8 {
		t.Fatal(len(files), err)
	}
	_ = s.Close()
	if _, err = s.List(context.Background(), "a"); !errors.Is(err, ErrClosed) {
		t.Fatal("closed store remained accessible")
	}
	_ = s.Close()
}
func TestFileCancellationAndInvalidConfiguration(t *testing.T) {
	if (Options{TTL: time.Second}).Validate() == nil {
		t.Fatal("short TTL")
	}
	if (Options{Path: "db"}).Validate() == nil {
		t.Fatal("plaintext persistence allowed")
	}
	s := testStore(t, false, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Put(ctx, "a", "x.txt", "user_data", "text/plain", []byte("a"), 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
