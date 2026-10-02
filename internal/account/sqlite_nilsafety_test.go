package account

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/oai-prism/oaiprism/internal/config"
)

// 回归测试（2026-10-03 CI 事故）：SQLite 初始化失败时 server 会拿到
// nil store 并继续运行；任何方法在 nil 接收者/零值实例上都不能 panic
// —— 记录请求日志发生在后台 goroutine 里，一次解引用就能崩掉进程。
func TestSQLiteStore_NilSafety(t *testing.T) {
	var nilStore *SQLiteStore
	zero := &SQLiteStore{} // db 未打开的零值实例

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	_ = log

	// nil 接收者：全部方法都不能 panic
	_ = nilStore.Close()
	if got := nilStore.Path(); got != "" {
		t.Fatalf("nil Path() 应为空串，得到 %q", got)
	}
	if _, err := nilStore.Load(); !errors.Is(err, errSQLiteUnavailable) {
		t.Fatalf("nil Load() 应返回 errSQLiteUnavailable，得到 %v", err)
	}
	if err := nilStore.RecordRequestLog(RequestLogItem{}); !errors.Is(err, errSQLiteUnavailable) {
		t.Fatalf("nil RecordRequestLog 应返回 errSQLiteUnavailable，得到 %v", err)
	}
	if _, _, err := nilStore.QueryRequestLogs(RequestLogFilter{}); !errors.Is(err, errSQLiteUnavailable) {
		t.Fatalf("nil QueryRequestLogs 应返回 errSQLiteUnavailable，得到 %v", err)
	}
	if _, err := nilStore.GetAggregatedStats(); !errors.Is(err, errSQLiteUnavailable) {
		t.Fatalf("nil GetAggregatedStats 应返回 errSQLiteUnavailable，得到 %v", err)
	}
	if _, err := nilStore.ListChatSessions(); !errors.Is(err, errSQLiteUnavailable) {
		t.Fatalf("nil ListChatSessions 应返回 errSQLiteUnavailable，得到 %v", err)
	}
	if _, err := nilStore.ListAPIKeys(); !errors.Is(err, errSQLiteUnavailable) {
		t.Fatalf("nil ListAPIKeys 应返回 errSQLiteUnavailable，得到 %v", err)
	}

	// 零值实例（db=nil）：同样的契约
	if _, err := zero.Load(); !errors.Is(err, errSQLiteUnavailable) {
		t.Fatalf("零值 Load() 应返回 errSQLiteUnavailable，得到 %v", err)
	}
	if err := zero.RecordRequestLog(RequestLogItem{}); !errors.Is(err, errSQLiteUnavailable) {
		t.Fatalf("零值 RecordRequestLog 应返回 errSQLiteUnavailable，得到 %v", err)
	}
}

// 正常路径不受 guard 影响：打开 -> 写入 -> 读回。
func TestSQLiteStore_RecordAndQuery(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "test.db"), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	item := RequestLogItem{ID: "r1", Method: "POST", Path: "/v1/chat/completions"}
	if err := store.RecordRequestLog(item); err != nil {
		t.Fatalf("RecordRequestLog: %v", err)
	}
	items, total, err := store.QueryRequestLogs(RequestLogFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("QueryRequestLogs: %v", err)
	}
	if total < 1 || len(items) < 1 || items[0].Path != "/v1/chat/completions" {
		t.Fatalf("查询结果异常: total=%d items=%+v", total, items)
	}

	// 账号 CRUD 冒烟
	acc := config.AccountConfig{ID: "a1", Name: "test"}
	if err := store.SaveAccount(acc); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	list, err := store.Load()
	if err != nil || len(list) == 0 {
		t.Fatalf("Load: err=%v list=%+v", err, list)
	}
}

// 并发回归（2026-10-03 CI 实证）：Close() 与后台写入竞争时不能 panic。
// 修复前 Close 在锁内把 s.db 置 nil，而写入方法的 ready() 检查在锁外 ——
// ready() 过后 db 被清空，exec(nil) 直接 panic。
func TestSQLiteStore_CloseRace(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "race.db"), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	// 写侧：持续写日志（模拟 requestAuditMiddleware 的后台 goroutine）
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				// 允许返回 errSQLiteUnavailable（Close 之后），但不能 panic
				_ = store.RecordRequestLog(RequestLogItem{Method: "POST", Path: "/v1/chat/completions"})
			}
		}
	}()
	// 关闭侧：与其他操作并发
	time.Sleep(20 * time.Millisecond)
	_ = store.Close()
	close(done)
	wg.Wait()

	// Close 之后再调用也必须安全
	if err := store.RecordRequestLog(RequestLogItem{}); !errors.Is(err, errSQLiteUnavailable) {
		t.Fatalf("Close 后 RecordRequestLog 应返回 errSQLiteUnavailable，得到 %v", err)
	}
	_ = store.Close() // 幂等
}
