package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestChunkedCompactionKeepsEverySummaryInsideBudget(t *testing.T) {
	var count atomic.Int32
	h := harness(t, func(ctx context.Context, q *Request, a func() error, e func(Delta) error) (*Result, error) {
		if !q.InternalSummary {
			t.Fatal("unexpected final generation during compaction")
		}
		n, err := RenderedTokens(q)
		if err != nil {
			t.Fatal(err)
		}
		if n > 4096-128-128 {
			t.Fatalf("summary exceeded model window: %d", n)
		}
		return summaryEngine(&count)(ctx, q, a, e)
	}, func(o *Options) { contextTestOptions(o); o.Context.WindowTokens = 4096 })
	q := contextRequest()
	if err := h.prepareContext(context.Background(), q, "owner", true, nil); err != nil {
		t.Fatal(err)
	}
	if count.Load() < 2 {
		t.Fatal("long context was not chunked", count.Load())
	}
	if q.ContextReport.EffectiveTokens > q.ContextReport.Budget {
		t.Fatal("final context still over budget")
	}
}
func TestSummaryCachePanicAndCancelledHitDoNotPoisonWaiters(t *testing.T) {
	cache := newSummaryCache()
	if _, _, err := cache.build(context.Background(), "key", time.Minute, func() (string, error) { panic("fixture panic") }); err == nil {
		t.Fatal("summary panic became success")
	}
	if len(cache.pending) != 0 || len(cache.entries) != 0 {
		t.Fatal("panic left a blocked cache entry")
	}
	if _, _, err := cache.build(context.Background(), "key", time.Minute, func() (string, error) { return "good", nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := cache.build(ctx, "key", time.Minute, func() (string, error) { t.Fatal("should not run"); return "", nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled cache hit accepted", err)
	}
	cache.clear()
	if _, _, err := cache.build(context.Background(), "new", time.Minute, func() (string, error) { return "should not run", nil }); err == nil {
		t.Fatal("closed cache accepted work")
	}
}
func TestAutomaticCompactionStoredItemsRetainPaginationIDs(t *testing.T) {
	var calls atomic.Int32
	h := harness(t, summaryEngine(&calls), func(o *Options) {
		contextTestOptions(o)
		o.ResponseStore = true
		o.Context.AutoCompact = true
		o.Context.TriggerTokens = 1000
	})
	q := contextRequest()
	body, _ := json.Marshal(map[string]any{"model": q.Model, "input": q.Items, "store": true})
	w := call(h, "POST", "/v1/responses", string(body), "key-a", "tenant")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	id := readMap(t, w)["id"].(string)
	page := call(h, "GET", "/v1/responses/"+id+"/input_items?order=asc", "", "key-a", "tenant")
	data := readMap(t, page)["data"].([]any)
	if len(data) == 0 {
		t.Fatal("missing compacted input history")
	}
	for _, item := range data {
		m := item.(map[string]any)
		if m["id"] == nil || m["id"] == "" {
			t.Fatal("summary item lost its API cursor ID", m)
		}
	}
}
