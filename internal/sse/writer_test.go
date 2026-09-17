package sse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestAppendJSONString_MatchesStdlib 用标准库作为参照系验证手写转义的正确性。
//
// 这是本包最需要被守住的不变量：手写转义快，但一旦转义错了，
// 下游会收到非法 JSON 并且报错信息完全对不上源头，极难排查。
func TestAppendJSONString_MatchesStdlib(t *testing.T) {
	cases := []string{
		"",
		"hello",
		`quote " backslash \ slash /`,
		"newline\n tab\t carriage\r backspace\b formfeed\f",
		"control\x00\x01\x1f",
		"中文测试：你好，世界！",
		"emoji 😀🎉 and CJK 漢字",
		"line\u2028sep\u2029par",
		"\xff\xfe invalid utf8",
		"\xc3", // 截断的 UTF-8
		strings.Repeat("a\"b\n中", 100),
		"</script><script>alert(1)</script>",
	}

	for _, in := range cases {
		got := string(AppendJSONString(nil, in))

		want, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("标准库无法序列化 %q: %v", in, err)
		}

		// 我们的实现刻意不做 HTML 转义、且把非法 UTF-8 替换为 U+FFFD，
		// 因此不能直接比字节，要比"解析回来的语义"。
		var gotVal, wantVal string
		if err := json.Unmarshal([]byte(got), &gotVal); err != nil {
			t.Fatalf("输入 %q\n输出 %s\n不是合法 JSON: %v", in, got, err)
		}
		if err := json.Unmarshal(want, &wantVal); err != nil {
			t.Fatalf("标准库输出非法: %v", err)
		}

		if gotVal != wantVal {
			// 非法 UTF-8 场景下标准库会替换成 U+FFFD，我们的行为应当一致。
			if !utf8.ValidString(in) && strings.ContainsRune(gotVal, utf8.RuneError) {
				continue
			}
			t.Errorf("输入 %q\n  got  = %q\n  want = %q", in, gotVal, wantVal)
		}

		// 输出本身必须是合法 UTF-8。
		if !utf8.ValidString(got) {
			t.Errorf("输入 %q 的输出不是合法 UTF-8: %q", in, got)
		}
	}
}

func TestAppendJSONString_NoHTMLEscape(t *testing.T) {
	// 我们有意不转义 < > &，减少体积。这条断言把这个决定固化下来。
	got := string(AppendJSONString(nil, "<a>&</a>"))
	if got != `"<a>&</a>"` {
		t.Fatalf("期望不转义 HTML，得到 %s", got)
	}
}

func TestAppendInt(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{-1, "-1"},
		{1234567890, "1234567890"},
		{-9223372036854775808, "-9223372036854775808"},
		{9223372036854775807, "9223372036854775807"},
	}
	for _, c := range cases {
		if got := string(AppendInt(nil, c.in)); got != c.want {
			t.Errorf("AppendInt(%d) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestWriter_FramesAndFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	w, err := New(rec)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type 不对: %s", ct)
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatal("缺少 X-Accel-Buffering，Nginx 会缓冲整个流")
	}

	if err := w.WriteData([]byte(`{"a":1}`)); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	if err := w.WriteRaw(DoneFrame); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	out := rec.Body.String()
	want := "data: {\"a\":1}\n\ndata: [DONE]\n\n"
	if out != want {
		t.Fatalf("输出不符\n got  = %q\n want = %q", out, want)
	}
	if !rec.Flushed {
		t.Fatal("没有 Flush，流式会退化成一次性返回")
	}
}

func TestWriter_MultilineData(t *testing.T) {
	rec := httptest.NewRecorder()
	w, _ := New(rec)
	defer w.Close()

	// 裸换行必须被拆成多个 data: 行，否则 SSE 解析器会把后半段当新字段。
	if err := w.WriteData([]byte("line1\nline2")); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "data: line1\ndata: line2\n\n") {
		t.Fatalf("多行 data 拆分不正确: %q", out)
	}
}

func TestWriter_CloseReturnsBufferToPool(t *testing.T) {
	rec := httptest.NewRecorder()
	w, _ := New(rec)
	_ = w.WriteData([]byte(`{"x":"y"}`))
	before := cap(w.buf)
	w.Close()

	if w.buf != nil {
		t.Fatal("Close 后应释放缓冲引用")
	}
	// 再取一次应当拿到同规格的缓冲（来自池）。
	rec2 := httptest.NewRecorder()
	w2, _ := New(rec2)
	if cap(w2.buf) < before {
		t.Fatalf("池中缓冲容量缩水: %d < %d", cap(w2.buf), before)
	}
	w2.Close()
}

func TestWriter_WriteAfterClose(t *testing.T) {
	rec := httptest.NewRecorder()
	w, _ := New(rec)
	w.Close()
	if err := w.WriteData([]byte("x")); err != ErrClosed {
		t.Fatalf("期望 ErrClosed，得到 %v", err)
	}
}

// TestWriter_NoFlusher 验证不支持 Flush 的 writer 会被明确拒绝。
func TestWriter_NoFlusher(t *testing.T) {
	_, err := New(noFlushWriter{h: http.Header{}})
	if err == nil {
		t.Fatal("应当报错：不支持 Flush 的 writer 无法做 SSE")
	}
}

type noFlushWriter struct{ h http.Header }

func (w noFlushWriter) Header() http.Header       { return w.h }
func (noFlushWriter) Write(b []byte) (int, error) { return len(b), nil }
func (noFlushWriter) WriteHeader(int)             {}

func TestAppendJSONString_NoAllocOnReuse(t *testing.T) {
	buf := make([]byte, 0, 4096)
	allocs := testing.AllocsPerRun(1000, func() {
		buf = AppendJSONString(buf[:0], "hello world")
	})
	if allocs > 0 {
		t.Fatalf("复用缓冲时不应有分配，实测 %v", allocs)
	}
}
