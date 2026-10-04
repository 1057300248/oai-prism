package facade

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/oai-prism/oaiprism/internal/account"
	"github.com/oai-prism/oaiprism/internal/gateway"
	"github.com/oai-prism/oaiprism/internal/prism"
)

func TestGatewayAccountEpochRejectsStaleOrCrossAccountResume(t *testing.T) {
	runner := &Runner{}
	accountA := &account.Account{ID: "a"}
	first := &RunRequest{Isolated: true}
	if err := runner.fenceGatewayAccount(accountA, first); err != nil || first.GatewayEpoch != 1 {
		t.Fatal(err, first.GatewayEpoch)
	}
	state := &gateway.UpstreamState{Epoch: first.GatewayEpoch, AccountID: "a", ProjectID: "project", ConversationID: "conversation", ResponseID: "response"}
	next := &RunRequest{Isolated: true, GatewayResume: state}
	if err := runner.fenceGatewayAccount(accountA, next); err != nil || next.GatewayEpoch != 2 || next.ProjectID != "project" || next.PreviousResponseID != "response" {
		t.Fatal("valid resume lost state", err)
	}
	// Another stateless request is sufficient to make the previous workspace unsafe.
	if err := runner.fenceGatewayAccount(accountA, &RunRequest{Isolated: true}); err != nil {
		t.Fatal(err)
	}
	stale := &RunRequest{Isolated: true, GatewayResume: &gateway.UpstreamState{Epoch: 2, AccountID: "a"}}
	var api *gateway.APIError
	if err := runner.fenceGatewayAccount(accountA, stale); !errors.As(err, &api) || api.Status != 409 {
		t.Fatal("stale sandbox reused", err)
	}
	if err := runner.fenceGatewayAccount(&account.Account{ID: "b"}, next); err == nil {
		t.Fatal("cross-account cursor reused")
	}
}
func TestCapturedGatewayCursorRequiresVerifiedBindingAndSession(t *testing.T) {
	req := &RunRequest{GatewayStateful: true, GatewayEpoch: 7, ConversationID: "conversation"}
	sb := &prism.Sandbox{URL: "https://prism.openai.com/s/sandboxes/proxy", Token: "secret"}
	result := &RunResult{AccountID: "a", ProjectID: "project", ConversationID: "conversation", ResponseID: "terminal-response", ListenSnapshot: json.RawMessage(`{"conversation_id":"conversation","project_id":"project","codex_session_id":"session","transcript_cursor":3,"sandbox_token":"secret"}`)}
	state := captureGatewayState(req, result, sb)
	if state == nil || state.Epoch != 7 || state.ResponseID != "terminal-response" {
		t.Fatal("valid snapshot rejected")
	}
	var snapshot map[string]any
	_ = json.Unmarshal(state.ListenSnapshot, &snapshot)
	if snapshot["sandbox_token"] != nil {
		t.Fatal("duplicated sandbox secret in snapshot")
	}
	for _, raw := range []string{`{"conversation_id":"other","project_id":"project","codex_session_id":"s","transcript_cursor":3}`, `{"conversation_id":"conversation","project_id":"project","codex_session_id":null,"transcript_cursor":3}`, `{"conversation_id":"conversation","project_id":"project","codex_session_id":"s","transcript_cursor":-1}`} {
		result.ListenSnapshot = json.RawMessage(raw)
		if captureGatewayState(req, result, sb) != nil {
			t.Fatal("invalid upstream cursor accepted", raw)
		}
	}
}
func TestGatewayStageErrorsPreserveCancellationButHideSecrets(t *testing.T) {
	wrapped := &gatewayStageError{"upload", context.Canceled}
	if !errors.Is(wrapped, context.Canceled) || wrapped.Error() != "gateway upstream stage failed: upload" {
		t.Fatal(wrapped)
	}
}
