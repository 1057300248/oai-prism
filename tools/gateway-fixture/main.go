// This binary is a protocol-test fixture, NOT a production upstream. It binds
// loopback only and never calls Prism or any paid model endpoint.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/oai-prism/oaiprism/internal/gateway"
)

type fixture struct{}

func (fixture) Run(ctx context.Context, q *gateway.Request, accepted func() error, emit func(gateway.Delta) error) (*gateway.Result, error) {
	if q.Model == "failure" {
		return nil, &gateway.APIError{Status: 429, Code: "rate_limit_exceeded", Message: "Fixture rate limit.", RetryAfter: time.Second}
	}
	if accepted != nil {
		if err := accepted(); err != nil {
			return nil, err
		}
	}
	if q.InternalSummary {
		return &gateway.Result{Text: "Goals: continue task. Facts: preserve constraints and src/main.go. Decisions: unchanged. Open work: next step.", Usage: &gateway.Usage{Input: 40, Output: 8, Source: "upstream"}}, nil
	}
	text := "Hello world"
	hasToolOutput := false
	for _, item := range q.Items {
		if item.Type == "function_call_output" {
			hasToolOutput = true
		}
	}
	if q.Format != "text" {
		text = `{"n":3}`
	}
	if len(q.Tools) > 0 {
		if hasToolOutput {
			text = `{"text":"The weather is sunny.","tool_calls":[]}`
		} else {
			text = `{"text":"","tool_calls":[{"name":"weather","arguments":{"city":"London"}}]}`
		}
	}
	if q.Model == "bad-schema" {
		text = `{"n":"wrong"}`
	}
	if emit != nil {
		if err := emit(gateway.Delta{Text: text}); err != nil {
			return nil, err
		}
	}
	if q.Model == "partial-failure" {
		return nil, errors.New("fixture upstream failure")
	}
	return &gateway.Result{Text: text, Usage: &gateway.Usage{Input: 12, Output: 4, Source: "upstream"}}, nil
}
func main() {
	h, err := gateway.New(gateway.Options{Context: gateway.ContextPolicy{Enabled: true, WindowTokens: 8192, OutputReserve: 512, SafetyMargin: 128, SummaryTokens: 128, KeepLastTurns: 1, SummaryCache: true}, Cache: gateway.PromptCachePolicy{Affinity: true, NativeModels: []string{"test-model"}}, Enabled: true, APIKeys: []string{"fixture-key"}, Models: map[string]string{"test-model": "fixture", "failure": "fixture", "partial-failure": "fixture", "bad-schema": "fixture"}, PromptTools: true, StructuredOutput: true, LocalOutputLimit: true, ResponseStore: true, TenantHeader: "X-Fixture-Tenant", TrustedPeers: []string{"127.0.0.1/32"}, Timeout: 5 * time.Second}, fixture{})
	if err != nil {
		log.Fatal(err)
	}
	defer h.Close()
	server := &http.Server{Addr: "127.0.0.1:18787", Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	log.Print("LOCAL TEST FIXTURE ONLY: 127.0.0.1:18787")
	log.Fatal(server.ListenAndServe())
}
