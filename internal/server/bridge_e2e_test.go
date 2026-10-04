package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/oai-prism/oaiprism/internal/config"
	"github.com/oai-prism/oaiprism/internal/gateway"
)

func TestBridgeRawMediaE2EUploadsBytesAndReferencesReturnedPath(t *testing.T) {
	for _, kind := range []string{"image", "pdf"} {
		t.Run(kind, func(t *testing.T) {
			body, data := mediaTestPayload(t, kind)
			fake := &fakeUpstream{t: t}
			base := fake.handler()
			var uploadedPath string
			var mu sync.Mutex
			upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/project-files/upload" {
					base.ServeHTTP(w, r)
					return
				}
				got, _ := io.ReadAll(r.Body)
				if !bytes.Equal(got, data) || strings.Contains(r.Header.Get("Content-Type"), "multipart") {
					t.Error("upload did not use raw bytes")
				}
				if r.Header.Get("X-Prism-Project-Id") == "" || r.Header.Get("X-Prism-Require-Project-Edit-Access") != "true" {
					t.Error("missing raw upload binding")
				}
				path := "/prism-uploads/" + r.Header.Get("X-Prism-File-Name")
				mu.Lock()
				uploadedPath = path
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"projectPath": path})
			})
			ts, _ := gatewayE2E(t, upstream, func(c *config.Config) { mediaTestConfig(c); c.Facade.Gateway.Bridge.UploadMode = "raw" })
			resp, err := mediaRequest(context.Background(), ts.URL+"/v1/responses", body)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("%d %s", resp.StatusCode, raw)
			}
			mu.Lock()
			path := uploadedPath
			mu.Unlock()
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if len(fake.startBodies) != 1 {
				t.Fatal("missing generation")
			}
			start, _ := json.Marshal(fake.startBodies[0])
			if path == "" || !bytes.Contains(start, []byte(path)) {
				t.Fatal("uploaded project path not carried into start", string(start), path)
			}
		})
	}
}
func TestBridgeExactStartBudgetRejectsPrivateEnvelopeBeforeGeneration(t *testing.T) {
	fake := &fakeUpstream{t: t}
	ts, _ := gatewayE2E(t, fake.handler(), func(c *config.Config) {
		c.Facade.Gateway.Bridge.MaxStartBytes = 4096
		c.Facade.Schema.FieldExtra = map[string]any{"operator_padding": strings.Repeat("x", 5000)}
	})
	resp, body := gatewayPost(t, ts.URL+"/v1/responses", `{"model":"instant","input":"small public input","store":false}`)
	if resp.StatusCode != 400 || !strings.Contains(body, "context_length_exceeded") || strings.Contains(body, "operator_padding") {
		t.Fatal(resp.StatusCode, body)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.startBodies) != 0 {
		t.Fatal("oversized exact body reached generation")
	}
}
func TestBridgeStoredContinuationActualStartDeltaAndAccountInterruption(t *testing.T) {
	fake := &fakeUpstream{t: t}
	base := fake.handler()
	var mu sync.Mutex
	starts := []map[string]any{}
	conversations, sandboxes := 0, 0
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/" && r.Method == "POST":
			mu.Lock()
			conversations++
			n := conversations
			mu.Unlock()
			w.Header().Set("Content-Type", "text/x-component")
			fmt.Fprintf(w, "1:%q\n", fmt.Sprintf("cdx1_00000000-0000-0000-0000-%012d", n))
		case r.URL.Path == "/api/backend/1/new":
			mu.Lock()
			sandboxes++
			n := sandboxes
			mu.Unlock()
			fmt.Fprintf(w, `{"url":"https://prism.openai.com/s/sandboxes/proxy","token":"sandbox-%d"}`, n)
		case strings.HasSuffix(r.URL.Path, "/sandbox/resources-token"):
			_, _ = w.Write([]byte(`{"access_token":"resource-token","max_age_seconds":3600}`))
		case r.URL.Path == "/api/y":
			_, _ = w.Write([]byte(`{"token":"ysweet-token","url":"wss://prism.openai.com/y/mock","baseUrl":"https://prism.openai.com/y/mock"}`))
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			_, _ = w.Write([]byte("OK"))
		case strings.HasSuffix(r.URL.Path, "/wait-for-sync"):
			_, _ = w.Write([]byte(`{"status":"synced","tokens":{"hasCurrentYSweetToken":true}}`))
		case strings.HasSuffix(r.URL.Path, "/resources-token") || strings.HasSuffix(r.URL.Path, "/token"):
			_, _ = w.Write([]byte(`{"success":true}`))
		case r.URL.Path == "/api/llm/response_with_tools_start":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			starts = append(starts, body)
			n := len(starts)
			mu.Unlock()
			meta, _ := body["metadata"].(map[string]any)
			cid, _ := body["conversationId"].(string)
			project, _ := meta["projectId"].(string)
			snapshot := map[string]any{"conversation_id": cid, "project_id": project, "codex_session_id": "sandbox-session", "transcript_cursor": n}
			payload := map[string]any{"id": fmt.Sprintf("provider-response-%d", n), "conversationId": cid, "codex_listen_snapshot": snapshot, "usage": map[string]any{"input_tokens": 20, "output_tokens": 4}, "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": fmt.Sprintf("answer-%d", n)}}}}}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "request_id": fmt.Sprintf("poll-%d", n), "response": map[string]any{"status": "success", "payload": payload}})
		default:
			base.ServeHTTP(w, r)
		}
	})
	ts, _ := gatewayE2E(t, upstream, func(c *config.Config) {
		c.Facade.UseSandbox = true
		c.Facade.Gateway.ResponseStore = true
		c.Facade.Gateway.TenantHeader = "X-Test-Tenant"
		c.Facade.Gateway.TrustedPeers = []string{"127.0.0.1/32"}
		c.Facade.Gateway.Bridge.Continuation = gateway.ContinuationPolicy{Enabled: true, VerifiedModels: []string{"instant"}, ActionID: "0123456789abcdef0123456789abcdef"}
	})
	request := func(input, previous string, store bool) (int, map[string]any) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"model": "instant", "input": input, "store": store, "previous_response_id": previous})
		resp, err := mediaRequest(context.Background(), ts.URL+"/v1/responses", body)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return resp.StatusCode, result
	}
	status, first := request("FIRST_HISTORY_MARKER", "", true)
	if status != 200 {
		t.Fatal(status, first)
	}
	firstID, _ := first["id"].(string)
	status, second := request("SECOND_NEW_MARKER", firstID, true)
	if status != 200 {
		t.Fatal(status, second)
	}
	secondID, _ := second["id"].(string)
	mu.Lock()
	if len(starts) != 2 || conversations != 1 || sandboxes != 1 {
		t.Fatal("continuation rebuilt workspace", len(starts), conversations, sandboxes)
	}
	raw, _ := json.Marshal(starts[1])
	previous := starts[1]["previousResponseId"]
	mu.Unlock()
	if previous != "provider-response-1" || bytes.Contains(raw, []byte("FIRST_HISTORY_MARKER")) || !bytes.Contains(raw, []byte("SECOND_NEW_MARKER")) {
		t.Fatal("wrong upstream delta", string(raw))
	}
	if status, _ = request("INTERVENING_STATELESS_REQUEST", "", false); status != 200 {
		t.Fatal("intervening request failed")
	}
	status, result := request("THIRD_NEW_MARKER", secondID, true)
	if status != 409 {
		t.Fatal("old workspace survived account interruption", status, result)
	}
	status, result = request("THIRD_NEW_MARKER", secondID, true)
	if status != 200 {
		t.Fatal("explicit retry could not rebuild full history", status, result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 4 {
		t.Fatal("stale request was executed or replayed", len(starts))
	}
	raw, _ = json.Marshal(starts[3])
	if !bytes.Contains(raw, []byte("FIRST_HISTORY_MARKER")) || !bytes.Contains(raw, []byte("THIRD_NEW_MARKER")) {
		t.Fatal("safe rebuild lost history", string(raw))
	}
}
