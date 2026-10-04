package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type engineFunc func(context.Context, *Request, func() error, func(Delta) error) (*Result, error)

func (f engineFunc) Run(ctx context.Context, q *Request, a func() error, e func(Delta) error) (*Result, error) {
	return f(ctx, q, a, e)
}
func options() Options {
	return Options{Enabled: true, APIKeys: []string{"key-a", "key-b"}, Models: map[string]string{"test-model": "test"}, PromptTools: true, StructuredOutput: true, LocalOutputLimit: true, InlineImages: true}
}
func harness(t *testing.T, engine engineFunc, tune func(*Options)) *Handler {
	t.Helper()
	o := options()
	if tune != nil {
		tune(&o)
	}
	h, err := New(o, engine)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}
func okEngine(ctx context.Context, q *Request, a func() error, e func(Delta) error) (*Result, error) {
	if a != nil {
		if err := a(); err != nil {
			return nil, err
		}
	}
	if e != nil {
		for _, text := range []string{"Hello ", "世界"} {
			if err := e(Delta{Text: text}); err != nil {
				return nil, err
			}
		}
	}
	cached, reasoning := 2, 1
	return &Result{Text: "Hello 世界", Usage: &Usage{Input: 10, Output: 4, Cached: &cached, Reasoning: &reasoning, Source: "upstream"}}, nil
}
func call(h http.Handler, method, path, body, key, tenant string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:9000"
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if tenant != "" {
		r.Header.Set("X-Test-Tenant", tenant)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func events(t *testing.T, body string) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m); err != nil {
			t.Fatalf("invalid SSE: %s", line)
		}
		out = append(out, m)
	}
	return out
}
func readMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return m
}

func TestResponsesStreamLifecycle(t *testing.T) {
	h := harness(t, okEngine, nil)
	w := call(h, "POST", "/v1/responses", `{"model":"test-model","input":"hi","stream":true,"store":false}`, "key-a", "")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	ev := events(t, w.Body.String())
	if len(ev) != 10 {
		t.Fatalf("events=%d %s", len(ev), w.Body)
	}
	id, item, text := "", "", ""
	terminals := 0
	for i, e := range ev {
		if e["sequence_number"] != float64(i) {
			t.Fatal("nonmonotonic sequence", e)
		}
		if r, ok := e["response"].(map[string]any); ok {
			if id == "" {
				id = r["id"].(string)
			}
			if r["id"] != id {
				t.Fatal("response ID changed")
			}
		}
		if output, ok := e["item"].(map[string]any); ok {
			if item == "" {
				item = output["id"].(string)
			}
			if output["id"] != item {
				t.Fatal("item ID changed")
			}
		}
		if e["type"] == "response.output_text.delta" {
			text += e["delta"].(string)
		}
		if e["type"] == "response.completed" {
			terminals++
			if i != len(ev)-1 {
				t.Fatal("event after terminal")
			}
			r := e["response"].(map[string]any)
			if r["output"].([]any)[0].(map[string]any)["id"] != item {
				t.Fatal("final item changed")
			}
		}
	}
	if text != "Hello 世界" || terminals != 1 || !strings.HasPrefix(id, "resp_") {
		t.Fatalf("text=%q id=%s terminals=%d", text, id, terminals)
	}
	if strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatal("Responses used Chat terminator")
	}
	if w.Result().Trailer.Get("X-Oaiprism-Usage-Source") != "upstream" {
		t.Fatal("missing usage source trailer", w.Result().Trailer)
	}
}
func TestChatStreamUsageAndFinish(t *testing.T) {
	h := harness(t, okEngine, nil)
	for _, include := range []bool{false, true} {
		w := call(h, "POST", "/v1/chat/completions", fmt.Sprintf(`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":%t}}`, include), "key-a", "")
		ev := events(t, w.Body.String())
		if len(ev) < 4 {
			t.Fatal(w.Body.String())
		}
		id := ev[0]["id"]
		usage, finish := 0, 0
		for _, e := range ev {
			if e["id"] != id {
				t.Fatal("unstable ID")
			}
			choices := e["choices"].([]any)
			if len(choices) == 0 {
				usage++
			} else if choices[0].(map[string]any)["finish_reason"] == "stop" {
				finish++
			}
			if !include {
				if _, ok := e["usage"]; ok {
					t.Fatal("usage present without include_usage")
				}
			}
		}
		want := 0
		if include {
			want = 1
		}
		if usage != want || finish != 1 || strings.Count(w.Body.String(), "data: [DONE]") != 1 {
			t.Fatal(w.Body.String())
		}
	}
}
func TestRejectBeforeEngine(t *testing.T) {
	calls := 0
	h := harness(t, func(context.Context, *Request, func() error, func(Delta) error) (*Result, error) {
		calls++
		return nil, errors.New("must not run")
	}, nil)
	cases := []struct {
		body   string
		status int
	}{
		{`null`, 400}, {`[]`, 400}, {`true`, 400}, {`{} {}`, 400},
		{`{"model":"test-model","model":"x","input":"x"}`, 400},
		{`{"model":"test-model","Model":"x","input":"x"}`, 400},
		{`{"model":"unknown","input":"x"}`, 404}, {`{"input":"x"}`, 400},
		{`{"model":"test-model","input":false}`, 400}, {`{"model":"test-model","input":42}`, 400},
		{`{"model":"test-model","input":"x","stream":null}`, 400},
		{`{"model":"test-model","input":"x","temperature":0}`, 400},
		{`{"model":"test-model","input":"x","store":true}`, 400},
		{`{"model":"test-model","input":"x","previous_response_id":"resp_other"}`, 400},
		{`{"model":"test-model","input":"x","max_output_tokens":0}`, 400},
		{`{"model":"test-model","input":"x","background":true}`, 400},
		{`{"model":"test-model","input":[{"type":"item_reference","id":"secret"}]}`, 400},
		{`{"model":"test-model","input":[{"role":"user","content":[{"type":"input_image","image_url":"http://127.0.0.1/secret"}]}]}`, 400},
		{`{"model":"test-model","input":"x","tools":[{"type":"web_search"}]}`, 400},
		{`{"model":"test-model","input":"x","metadata":{"key":{}}}`, 400},
		{`{"model":"test-model","input":[{"type":"function_call_output","call_id":"fabricated","output":"ok"}]}`, 400},
	}
	for _, test := range cases {
		w := call(h, "POST", "/v1/responses", test.body, "key-a", "")
		if w.Code != test.status {
			t.Errorf("%s got %d %s", test.body, w.Code, w.Body)
		}
		if readMap(t, w)["error"] == nil {
			t.Error("missing OpenAI error")
		}
	}
	if calls != 0 {
		t.Fatal("invalid input reached engine", calls)
	}
}
func TestInitialAndPartialFailures(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, context.Canceled, errors.New("private upstream credential"), &APIError{Status: 429, Code: "rate_limit_exceeded", Message: "Retry later.", RetryAfter: 2 * time.Second}} {
		for _, partial := range []bool{false, true} {
			h := harness(t, func(ctx context.Context, q *Request, a func() error, e func(Delta) error) (*Result, error) {
				if partial {
					if err := a(); err != nil {
						return nil, err
					}
					if err := e(Delta{Text: "partial"}); err != nil {
						return nil, err
					}
				}
				return nil, failure
			}, nil)
			for _, responses := range []bool{false, true} {
				path, body := "/v1/responses", `{"model":"test-model","input":"x","stream":true}`
				if !responses {
					path, body = "/v1/chat/completions", `{"model":"test-model","messages":[{"role":"user","content":"x"}],"stream":true}`
				}
				w := call(h, "POST", path, body, "key-a", "")
				text := w.Body.String()
				if strings.Contains(text, "private upstream") || strings.Contains(text, "response.completed") || strings.Contains(text, "[DONE]") || strings.Contains(text, `"finish_reason":"stop"`) {
					t.Fatalf("false success/leak: %s", text)
				}
				if partial {
					if w.Code != 200 {
						t.Fatal("headers should already be committed")
					}
					if responses && !strings.Contains(text, "response.failed") {
						t.Fatal("no terminal failure")
					}
				} else if w.Code != publicError(failure).Status {
					t.Fatalf("expected HTTP failure: %d %s", w.Code, text)
				}
			}
		}
	}
}
func TestTextNeverBecomesImplicitSession(t *testing.T) {
	h := harness(t, func(ctx context.Context, q *Request, a func() error, e func(Delta) error) (*Result, error) {
		if len(q.Items) != 1 || len(q.Items[0].Content) != 1 || q.Items[0].Content[0].Text != "你好 hello" {
			t.Fatalf("string split or cached history: %+v", q.Items)
		}
		return okEngine(ctx, q, a, e)
	}, nil)
	body := `{"model":"test-model","input":"你好 hello","user":"same","prompt_cache_key":"same","metadata":{"session_id":"same"}}`
	for i := 0; i < 2; i++ {
		w := call(h, "POST", "/v1/responses", body, "key-a", "")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if len(h.store.memory) != 0 {
		t.Fatal("store=false retained state")
	}
}
func TestSnapshotOwnershipAndBranching(t *testing.T) {
	var lengths []int
	h := harness(t, func(ctx context.Context, q *Request, a func() error, e func(Delta) error) (*Result, error) {
		lengths = append(lengths, len(q.Items))
		if len(lengths) > 1 && q.Instructions != "" {
			t.Fatal("previous instructions carried into next response")
		}
		return okEngine(ctx, q, a, e)
	}, func(o *Options) {
		o.ResponseStore = true
		o.TenantHeader = "X-Test-Tenant"
		o.TrustedPeers = []string{"127.0.0.1/32"}
	})
	first := call(h, "POST", "/v1/responses", `{"model":"test-model","input":"first","instructions":"first-only","store":true}`, "key-a", "tenant-a")
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	id := readMap(t, first)["id"].(string)
	for _, identity := range [][2]string{{"key-a", "tenant-b"}, {"key-b", "tenant-a"}} {
		for _, method := range []string{"GET", "DELETE"} {
			w := call(h, method, "/v1/responses/"+id, "", identity[0], identity[1])
			if w.Code != 404 {
				t.Fatal("cross-tenant access", method, w.Code)
			}
		}
		w := call(h, "POST", "/v1/responses", fmt.Sprintf(`{"model":"test-model","input":"x","previous_response_id":%q}`, id), identity[0], identity[1])
		if w.Code != 404 {
			t.Fatal("cross-tenant continuation")
		}
	}
	for i := 0; i < 2; i++ {
		w := call(h, "POST", "/v1/responses", fmt.Sprintf(`{"model":"test-model","input":"branch","previous_response_id":%q,"store":false}`, id), "key-a", "tenant-a")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if fmt.Sprint(lengths) != "[1 3 3]" {
		t.Fatalf("snapshot mutated or history lost: %v", lengths)
	}
	if len(h.store.memory) != 1 {
		t.Fatal("store=false saved response")
	}
	if call(h, "DELETE", "/v1/responses/"+id, "", "key-a", "tenant-a").Code != 200 {
		t.Fatal("delete failed")
	}
	if call(h, "GET", "/v1/responses/"+id, "", "key-a", "tenant-a").Code != 404 {
		t.Fatal("deleted response accessible")
	}
}
func TestUntrustedTenantAndPrivateHeaders(t *testing.T) {
	h := harness(t, okEngine, func(o *Options) {
		o.ResponseStore = true
		o.TenantHeader = "X-Test-Tenant"
		o.TrustedPeers = []string{"127.0.0.1/32"}
	})
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test-model","input":"x"}`))
	r.RemoteAddr = "203.0.113.9:9000"
	r.Header.Set("Authorization", "Bearer key-a")
	r.Header.Set("X-Test-Tenant", "tenant")
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("trusted X-Forwarded-For")
	}
	h = harness(t, okEngine, nil)
	for _, header := range []string{"X-Local-Workspace", "X-Oaiprism-Model", "X-Oaiprism-Account", "X-Prism-Conversation-ID", "Openai-Sentinel-Token"} {
		r = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test-model","input":"x"}`))
		r.Header.Set("Authorization", "Bearer key-a")
		r.Header.Set(header, "private")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Errorf("accepted %s", header)
		}
	}
	for _, path := range []string{"/admin/accounts", "/prism/api/echo", "/v1/audio/speech"} {
		if call(h, "GET", path, "", "key-a", "").Code != 404 {
			t.Fatal("unexpected route", path)
		}
	}
}

const toolRequest = `{"model":"test-model","input":"weather?","tools":[{"type":"function","name":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false},"strict":true}],"tool_choice":"required","parallel_tool_calls":false}`

func TestToolSchemaAndStreamingCalls(t *testing.T) {
	for _, valid := range []bool{true, false} {
		h := harness(t, func(ctx context.Context, q *Request, a func() error, e func(Delta) error) (*Result, error) {
			if a != nil {
				if err := a(); err != nil {
					return nil, err
				}
			}
			text := `{"text":"","tool_calls":[{"name":"weather","arguments":{"city":"London"}}]}`
			if !valid {
				text = `{"text":"","tool_calls":[{"name":"weather","arguments":{"city":42}}]}`
			}
			if e != nil {
				if err := e(Delta{Text: text}); err != nil {
					return nil, err
				}
			}
			return &Result{Text: text, Usage: &Usage{Input: 10, Output: 9, Source: "upstream"}}, nil
		}, nil)
		var body map[string]any
		_ = json.Unmarshal([]byte(toolRequest), &body)
		body["stream"] = true
		raw, _ := json.Marshal(body)
		w := call(h, "POST", "/v1/responses", string(raw), "key-a", "")
		ev := events(t, w.Body.String())
		if !valid {
			if !strings.Contains(w.Body.String(), "response.failed") || strings.Contains(w.Body.String(), "response.completed") {
				t.Fatal(w.Body.String())
			}
			continue
		}
		delta, done := "", ""
		id := ""
		for _, e := range ev {
			if e["type"] == "response.function_call_arguments.delta" {
				delta = e["delta"].(string)
			}
			if e["type"] == "response.function_call_arguments.done" {
				done = e["arguments"].(string)
			}
			if item, ok := e["item"].(map[string]any); ok {
				if id == "" {
					id = item["id"].(string)
				}
				if item["id"] != id {
					t.Fatal("tool item ID changed")
				}
			}
		}
		if delta != `{"city":"London"}` || done != delta {
			t.Fatal("arguments contract", delta, done)
		}
		final := ev[len(ev)-1]["response"].(map[string]any)
		item := final["output"].([]any)[0].(map[string]any)
		if item["type"] != "function_call" || !strings.HasPrefix(item["call_id"].(string), "call_") {
			t.Fatal(item)
		}
	}
}
func TestStructuredOutputAndLocalLimit(t *testing.T) {
	for _, valid := range []bool{true, false} {
		h := harness(t, func(context.Context, *Request, func() error, func(Delta) error) (*Result, error) {
			text := `{"n":3}`
			if !valid {
				text = `{"n":"three"}`
			}
			return &Result{Text: text}, nil
		}, nil)
		w := call(h, "POST", "/v1/responses", `{"model":"test-model","input":"x","text":{"format":{"type":"json_schema","name":"number","schema":{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false},"strict":true}}}`, "key-a", "")
		expected := 200
		if !valid {
			expected = 502
		}
		if w.Code != expected {
			t.Fatalf("schema: %d %s", w.Code, w.Body)
		}
	}
	h := harness(t, okEngine, nil)
	w := call(h, "POST", "/v1/responses", `{"model":"test-model","input":"x","max_output_tokens":1,"stream":true}`, "key-a", "")
	if !strings.Contains(w.Body.String(), "response.incomplete") || strings.Contains(w.Body.String(), "response.completed") {
		t.Fatal("local cap terminal", w.Body.String())
	}
	if w.Header().Get("X-Oaiprism-Output-Limit") != "local-visible-o200k_base" {
		t.Fatal("local limit not disclosed")
	}
}
func TestSchemaReferencesCannotReadFiles(t *testing.T) {
	for _, schema := range []string{`{"$ref":"file:///etc/passwd"}`, `{"$ref":"http://127.0.0.1/internal"}`, `{"$id":"file:///tmp/override","type":"string"}`} {
		if _, err := compileSchema([]byte(schema)); err == nil {
			t.Fatal("external schema resource accepted")
		}
	}
}
func TestUsagePolicyAndShortText(t *testing.T) {
	q := &Request{Items: []Item{{Type: "message", Role: "user", Content: []Content{{Type: "input_text", Text: "x"}}}}}
	u, err := NormalizeUsage(q, &Result{Text: "a"}, "estimate")
	if err != nil || u.Output < 1 || u.Source != "estimated" || u.Cached != nil {
		t.Fatalf("estimate=%+v %v", u, err)
	}
	if _, err = NormalizeUsage(q, &Result{Text: "a"}, "upstream_only"); err == nil {
		t.Fatal("invented authoritative usage")
	}
	badCache := 11
	if _, err = NormalizeUsage(q, &Result{Usage: &Usage{Input: 10, Output: 1, Cached: &badCache, Source: "upstream"}}, "estimate"); err == nil {
		t.Fatal("invalid cache accepted")
	}
}
func TestConcurrencyAndDeadline(t *testing.T) {
	h := harness(t, okEngine, func(o *Options) { o.MaxConcurrent = 64 })
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := call(h, "POST", "/v1/responses", `{"model":"test-model","input":"x","stream":true}`, "key-a", "")
			if w.Code != 200 || strings.Count(w.Body.String(), "event: response.completed\n") != 1 {
				t.Errorf("bad concurrent stream %d", w.Code)
			}
		}()
	}
	wg.Wait()
	h = harness(t, func(ctx context.Context, _ *Request, _ func() error, _ func(Delta) error) (*Result, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, func(o *Options) { o.Timeout = 20 * time.Millisecond })
	if w := call(h, "POST", "/v1/responses", `{"model":"test-model","input":"x"}`, "key-a", ""); w.Code != 504 {
		t.Fatal("deadline not applied", w.Code)
	}
}
