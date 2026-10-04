package gateway

import (
	"context"
	"strings"
	"testing"
)

func TestSummaryInheritsBridgeBudgetWithoutUpstreamCursor(t *testing.T) {
	q := &Request{Model: "test-model", Bridge: BridgePolicy{MaxStartBytes: 4096, InstructionPlacement: "user_relay", UploadMode: "raw", Continuation: ContinuationPolicy{Enabled: true}}, UpstreamState: cursorFixture(), ContinuationOffset: 7, StoredConversation: true}
	sq := summaryRequest(q, nil, []Item{{Type: "message", Role: "user", Content: []Content{{Type: "input_text", Text: strings.Repeat("x", 5000)}}}}, "", 128)
	if sq.Bridge.MaxStartBytes != 4096 || sq.Bridge.UploadMode != "raw" || sq.Bridge.InstructionPlacement != "user_relay" {
		t.Fatal("summary lost transport policy")
	}
	if !sq.InternalSummary || sq.StoredConversation || sq.UpstreamState != nil || sq.ContinuationOffset != 0 || sq.Store {
		t.Fatal("summary inherited a mutable conversation cursor")
	}
	if CheckTransportBudget(sq) == nil {
		t.Fatal("summary escaped transport byte cap")
	}
}
func TestSummaryDoesNotStartWhenIndivisibleTurnExceedsByteBudget(t *testing.T) {
	calls := 0
	h := harness(t, func(context.Context, *Request, func() error, func(Delta) error) (*Result, error) {
		calls++
		return &Result{Text: "summary"}, nil
	}, nil)
	h.options.Context.Enabled = true
	h.options.Context.WindowTokens = 32768
	h.options.Context.KeepLastTurns = 1
	h.options.Context.SummaryTokens = 128
	q := &Request{Model: "test-model", Format: "text", Bridge: BridgePolicy{MaxStartBytes: 4096}, Items: []Item{
		{Type: "message", Role: "user", Content: []Content{{Type: "input_text", Text: strings.Repeat("long_old_payload", 600)}}},
		{Type: "message", Role: "assistant", Content: []Content{{Type: "output_text", Text: "old reply"}}},
		{Type: "message", Role: "user", Content: []Content{{Type: "input_text", Text: "new question"}}},
	}}
	before := continuationHistoryHash(q.Items)
	if err := h.prepareContext(context.Background(), q, "owner", true, nil); err == nil {
		t.Fatal("oversized summary turn accepted")
	}
	if calls != 0 || continuationHistoryHash(q.Items) != before {
		t.Fatal("summary started or history changed despite budget refusal", calls)
	}
}
func TestContinuationCannotBeRecreatedAfterRegistryClose(t *testing.T) {
	h := continuationHarness(t)
	q := continuationRequest(h)
	lease, err := h.beginContinuation(q, "owner", "resp-one", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.continuations.clear()
	lease.complete(q, &Result{UpstreamState: cursorFixture()}, answered(q))
	if len(h.continuations.records) != 0 || len(h.continuations.sessions) != 0 {
		t.Fatal("in-flight completion reopened closed registry")
	}
	if _, err = h.beginContinuation(continuationRequest(h), "owner", "resp-two", nil); err == nil || publicError(err).Status != 503 {
		t.Fatal("closed registry accepted new state", err)
	}
}
