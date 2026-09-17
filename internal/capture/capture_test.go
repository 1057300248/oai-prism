package capture

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oai-prism/oaiprism/internal/prism"
)

func TestRecorder_DisabledIsNil(t *testing.T) {
	// 关闭时返回 nil 是刻意的：调用方只需 `if rec != nil`，
	// 关闭状态下零开销，不需要在每个热路径分支里判 Enabled。
	if r := New(Options{Enabled: false}, nil); r != nil {
		t.Fatal("未启用时应当返回 nil")
	}
	var nilRec *Recorder
	if nilRec.Enabled() {
		t.Fatal("nil Recorder 的 Enabled 应为 false")
	}
	// 所有方法在 nil 接收者上都必须是安全的空操作。
	nilRec.RecordRequest(prism.Meta{Path: "/x"}, nil, []byte("body"))
	nilRec.RecordRaw("request", "GET", "/x", 200, "a", nil, nil)
	nilRec.Close()
	if nilRec.Dropped() != 0 || nilRec.Written() != 0 {
		t.Fatal("nil Recorder 的计数应为 0")
	}
}

func TestRecorder_RedactsSensitiveHeaders(t *testing.T) {
	dir := t.TempDir()
	r := New(Options{Enabled: true, Dir: dir, Redact: true, Sample: 1}, nil)
	if r == nil {
		t.Fatal("应当创建成功")
	}
	defer r.Close()

	hdr := http.Header{
		"Authorization": {"Bearer super-secret-token"},
		"Cookie":        {"__Secure-next-auth.session-token=xyz"},
		"Set-Cookie":    {"session=abc"},
		"X-Api-Key":     {"sk-secret"},
		"X-Normal":      {"keep"},
	}
	r.RecordRequest(prism.Meta{Method: "POST", Path: "/api/lim/response_with_tools_start", Started: time.Now()},
		hdr, []byte(`{"model":"gpt-5"}`))
	r.Close()

	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("应当生成 1 个抓包文件，得到 %v (%v)", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, leaked := range []string{"super-secret-token", "session-token=xyz", "session=abc", "sk-secret"} {
		if strings.Contains(content, leaked) {
			t.Fatalf("严重：抓包文件泄漏了凭据 %q\n%s", leaked, content)
		}
	}
	if !strings.Contains(content, "***REDACTED***") {
		t.Error("应当出现打码标记")
	}
	if !strings.Contains(content, "keep") {
		t.Error("非敏感头应当保留")
	}
	if !strings.Contains(content, "gpt-5") {
		t.Error("请求体应当被记录")
	}
}

func TestRecorder_PathFilter(t *testing.T) {
	dir := t.TempDir()
	r := New(Options{
		Enabled:      true,
		Dir:          dir,
		Redact:       true,
		Sample:       1,
		ExcludePaths: []string{"/s/sandboxes/proxy/render"},
		IncludePaths: []string{"/api/"},
	}, nil)
	defer r.Close()

	r.RecordRequest(prism.Meta{Method: "POST", Path: "/api/projects", Started: time.Now()}, nil, []byte(`{}`))
	r.RecordRequest(prism.Meta{Method: "POST", Path: "/s/sandboxes/proxy/render", Started: time.Now()}, nil, []byte(`{}`))
	r.RecordRequest(prism.Meta{Method: "POST", Path: "/other/path", Started: time.Now()}, nil, []byte(`{}`))
	r.Close()

	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("文件数 = %d", len(files))
	}
	data, _ := os.ReadFile(files[0])
	content := string(data)

	if !strings.Contains(content, "/api/projects") {
		t.Error("白名单内的路径应当被记录")
	}
	if strings.Contains(content, "render") {
		t.Error("黑名单内的路径不应被记录")
	}
	if strings.Contains(content, "/other/path") {
		t.Error("白名单外的路径不应被记录")
	}
}

func TestRecorder_BodyTruncation(t *testing.T) {
	dir := t.TempDir()
	r := New(Options{Enabled: true, Dir: dir, Redact: true, MaxBody: 16, Sample: 1}, nil)
	defer r.Close()

	big := strings.Repeat("x", 1000)
	r.RecordRequest(prism.Meta{Method: "POST", Path: "/api/projects", Started: time.Now()}, nil, []byte(big))
	r.Close()

	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	data, _ := os.ReadFile(files[0])
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		if len(rec.Body) != 16 {
			t.Fatalf("body 应当被截断到 16 字节，得到 %d", len(rec.Body))
		}
	}
}

func TestRecorder_QueueOverflowDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	// 队列故意设得很小，然后灌入远超容量的记录。
	// 关键断言是：这个循环必须立刻返回，而不是被阻塞。
	r := New(Options{Enabled: true, Dir: dir, Redact: true, Sample: 1, QueueSize: 4}, nil)
	defer r.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5000; i++ {
			r.RecordRequest(prism.Meta{Method: "POST", Path: "/api/projects", Started: time.Now()}, nil, []byte(`{}`))
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("录制队列满时阻塞了调用方——这会让生产流量变慢")
	}
}

func TestReplayFile(t *testing.T) {
	dir := t.TempDir()
	dirFile := filepath.Join(dir, "capture-2026-01-01.jsonl")
	lines := []string{
		`{"time":"2026-01-01T00:00:00Z","direction":"request","method":"POST","path":"/api/projects","body":"{\"a\":1}"}`,
		`{"time":"2026-01-01T00:00:01Z","direction":"response","method":"POST","path":"/api/projects","status":200,"body":"{\"uuid\":\"p1\"}"}`,
	}
	if err := os.WriteFile(dirFile, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	recs, err := ReplayFile(dirFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("记录数 = %d", len(recs))
	}
	if recs[0].Direction != "request" || recs[1].Status != 200 {
		t.Fatalf("解析错误: %+v", recs)
	}
}

func TestReplayFile_BadLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.jsonl")
	_ = os.WriteFile(p, []byte("{not json}\n"), 0o600)
	if _, err := ReplayFile(p); err == nil {
		t.Fatal("非法行应当报错")
	}
}

// TestSummarize 验证"抓包 -> 协议字段表"这条链路。
//
// 这是本包最实用的功能：把真实抓包变成可以直接填进 YAML 的字段清单。
func TestSummarize(t *testing.T) {
	recs := []Record{
		{Direction: "request", Method: "POST", Path: "/api/lim/response_with_tools_start",
			Body: `{"model":"gpt-5","messages":[],"project_id":"abc"}`},
		{Direction: "response", Method: "POST", Path: "/api/lim/response_with_tools_start",
			Status: 200, Body: `{"id":"resp-1","status":"in_progress"}`},
		{Direction: "response", Method: "POST", Path: "/api/lim/response_with_tools_status",
			Status: 200, Body: `{"response_id":"resp-1","done":true,"output":{"text":"hi"}}`},
		// 带 UUID 的路径应当被归一化，避免同一端点产生多条摘要。
		{Direction: "response", Method: "PATCH",
			Path: "/api/projects/550e8400-e29b-41d4-a716-446655440000/thumbnail", Status: 200, Body: `{}`},
	}

	out := Summarize(recs)

	for _, want := range []string{
		"POST /api/lim/response_with_tools_start",
		"POST /api/lim/response_with_tools_status",
		"PATCH /api/projects/{id}/thumbnail",
		"model",
		"messages",
		"project_id",
		"response_id",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("摘要缺少 %q\n%s", want, out)
		}
	}
	// 原始 UUID 不应出现在摘要里。
	if strings.Contains(out, "550e8400") {
		t.Errorf("路径未被归一化:\n%s", out)
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"/api/projects/550e8400-e29b-41d4-a716-446655440000/thumbnail": "/api/projects/{id}/thumbnail",
		"/api/project-access?d=abc":                                    "/api/project-access",
		"/s/sandboxes/proxy/render-status":                             "/s/sandboxes/proxy/render-status",
	}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecorder_WritesJSONL(t *testing.T) {
	dir := t.TempDir()
	r := New(Options{Enabled: true, Dir: dir, Redact: true, Sample: 1}, nil)
	r.RecordResponse(prism.Meta{
		Method: "POST", Path: "/api/lim/response_with_tools_status",
		Status: 200, Started: time.Now(), Duration: 150 * time.Millisecond,
	}, http.Header{"Content-Type": {"application/json"}}, []byte(`{"ok":true}`))
	r.Close()

	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	data, _ := os.ReadFile(files[0])
	var rec Record
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Status != 200 || rec.DurationMS != 150 {
		t.Fatalf("元信息丢失: %+v", rec)
	}
	if rec.Direction != "response" {
		t.Fatalf("方向错误: %s", rec.Direction)
	}
}
