package server

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/oai-prism/oaiprism/internal/config"
)

func TestGatewayCacheControlsReachUpstreamAndUsage(t *testing.T) {
	fake := &fakeUpstream{t: t}
	base := fake.handler()
	var mu sync.Mutex
	starts := []map[string]any{}
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/llm/response_with_tools_start" {
			base.ServeHTTP(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		starts = append(starts, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"completed","request_id":"upstream-fixture","response":{"status":"success","payload":{"id":"private-upstream-response","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":100,"output_tokens":4,"total_tokens":104,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":40}}}}}`)
	})
	ts, _ := gatewayE2E(t, up, func(c *config.Config) {
		c.Facade.Gateway.Cache.Affinity = true
		c.Facade.Gateway.Cache.NativeModels = []string{"instant"}
	})
	request := `{"model":"instant","input":[{"role":"user","content":"first"},{"role":"assistant","content":"answer"},{"role":"user","content":"second"}],"prompt_cache_key":"raw-client-key","prompt_cache_options":{"mode":"implicit","ttl":"30m"}}`
	for i := 0; i < 2; i++ {
		resp, text := gatewayPost(t, ts.URL+"/v1/responses", request)
		if resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, text)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(text), &body); err != nil {
			t.Fatal(err)
		}
		detail := body["usage"].(map[string]any)["input_tokens_details"].(map[string]any)
		if detail["cached_tokens"] != float64(60) || detail["cache_write_tokens"] != float64(40) {
			t.Fatal("gateway dropped native cache details", detail)
		}
	}
	mu.Lock()
	if len(starts) != 2 {
		t.Fatal("not exactly two generations", len(starts))
	}
	key := starts[0]["prompt_cache_key"]
	if key == nil || key == "raw-client-key" || starts[1]["prompt_cache_key"] != key {
		t.Fatal("cache key discarded, unscoped or unstable", key)
	}
	if starts[0]["prompt_cache_options"].(map[string]any)["ttl"] != "30m" {
		t.Fatal("retention was not forwarded")
	}
	if !reflect.DeepEqual(starts[0]["input"], starts[1]["input"]) {
		t.Fatal("random API item IDs polluted repeated model input")
	}
	wire, _ := json.Marshal(starts[0]["input"])
	if strings.Contains(string(wire), "item_") {
		t.Fatal("gateway item ID entered prompt")
	}
	mu.Unlock()
	fake.mu.Lock()
	projects := fake.projectCount
	fake.mu.Unlock()
	if projects != 2 {
		t.Fatal("cache affinity reused mutable workspace", projects)
	}
	resp, _ := gatewayPost(t, ts.URL+"/v1/responses", `{"model":"test-model","input":"x","prompt_cache_options":{"ttl":"30m"}}`)
	if resp.StatusCode != 400 {
		t.Fatal("unverified model silently accepted native cache options", resp.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 2 {
		t.Fatal("rejected cache options reached upstream")
	}
}
