package account

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

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
