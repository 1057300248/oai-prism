package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (h *Handler) contextEndpoint(w http.ResponseWriter, r *http.Request, owner string, countOnly bool) {
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		h.fail(w, r, nil, &APIError{Status: 503, Code: "gateway_busy", Message: "Gateway concurrency capacity is exhausted."})
		return
	}
	timeout := h.options.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	for name := range r.Header {
		k := strings.ToLower(name)
		if strings.HasPrefix(k, "x-oaiprism-") || strings.HasPrefix(k, "x-prism-") || k == "x-local-workspace" || k == "openai-sentinel-token" || k == "x-openai-sentinel-token" {
			h.fail(w, r, nil, bad("headers", "Private upstream overrides are disabled."))
			return
		}
	}
	if ce := r.Header.Get("Content-Encoding"); ce != "" && !strings.EqualFold(ce, "identity") {
		h.fail(w, r, nil, &APIError{Status: 415, Code: "unsupported_content_encoding", Message: "Send uncompressed JSON."})
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || mt != "application/json" {
			h.fail(w, r, nil, &APIError{Status: 415, Code: "unsupported_media_type", Message: "Use application/json."})
			return
		}
	}
	reader := http.MaxBytesReader(w, r.Body, MaxBody)
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			err = &APIError{Status: 413, Code: "request_too_large", Message: "Request body exceeds gateway limit."}
		}
		h.fail(w, r, nil, err)
		return
	}
	fields, err := Object(body)
	if err != nil {
		h.fail(w, r, nil, err)
		return
	}
	if err = keys(fields, "model input instructions tools reasoning previous_response_id prompt_cache_key prompt_cache_retention prompt_cache_options"); err != nil {
		h.fail(w, r, nil, err)
		return
	}
	fields["store"] = json.RawMessage("false")
	body, _ = json.Marshal(fields)
	q, err := Parse(body, true, h.options)
	if err != nil {
		h.fail(w, r, nil, err)
		return
	}
	if q.PreviousID != "" {
		snapshot, ok, e := h.store.Get(ctx, q.PreviousID, owner)
		if e != nil {
			h.fail(w, r, nil, e)
			return
		}
		if !ok {
			h.fail(w, r, nil, missingResponse())
			return
		}
		q.Items = append(snapshot.Items, q.Items...)
	}
	raw, _ := json.Marshal(q.Items)
	if len(raw) > MaxHistory || len(q.Items) > MaxItems {
		h.fail(w, r, nil, contextTooLarge("History exceeds the gateway transport bound."))
		return
	}
	if _, _, _, _, err = partitionContext(q.Items, h.options.Context.keepTurns()); err != nil {
		h.fail(w, r, nil, err)
		return
	}
	h.bindPromptCache(q, owner)
	if countOnly {
		n, e := RenderedTokens(q)
		if e != nil {
			h.fail(w, r, nil, e)
			return
		}
		writeJSON(w, 200, map[string]any{"object": "response.input_tokens", "input_tokens": n, "x_oaiprism_source": "estimated", "x_oaiprism_tokenizer": "o200k_base_rendered"})
		return
	}
	if err = h.prepareContext(ctx, q, owner, true, nil); err != nil {
		h.fail(w, r, nil, withContextReport(q, err))
		return
	}
	if err = ctx.Err(); err != nil {
		h.fail(w, r, nil, withContextReport(q, err))
		return
	}
	output := append([]Item(nil), q.Items...)
	// Local summaries cannot manufacture provider-encrypted reasoning state. This
	// endpoint emits standard reusable message items with an explicit extension.
	if q.Instructions != "" {
		output = append([]Item{{Type: "message", Role: "system", Content: []Content{{Type: "input_text", Text: q.Instructions}}}}, output...)
	}
	for i := range output {
		if output[i].ID == "" {
			output[i].ID = "item_" + uuid.NewString()
		}
	}
	usage := q.ContextUsage
	if usage == nil {
		usage = &Usage{Source: "none"}
	} else {
		copy := *usage
		usage = &copy
	}
	usage.Context = q.ContextReport
	w.Header().Set("X-Oaiprism-Compaction", "gateway-summary-not-native")
	setContextHeaders(w, q)
	writeJSON(w, 200, map[string]any{"id": "cmp_" + uuid.NewString(), "object": "response.compaction", "created_at": time.Now().Unix(), "output": output, "usage": usageJSON(usage, true), "x_oaiprism_context": q.ContextReport, "x_oaiprism_native_compaction": false})
}
func setContextHeaders(w http.ResponseWriter, q *Request) {
	if q.ContextReport == nil {
		return
	}
	c := q.ContextReport
	w.Header().Set("X-Oaiprism-Context-Before", strconv.Itoa(c.OriginalTokens))
	w.Header().Set("X-Oaiprism-Context-After", strconv.Itoa(c.EffectiveTokens))
	w.Header().Set("X-Oaiprism-Summary-Calls", strconv.Itoa(c.SummaryCalls))
	w.Header().Set("X-Oaiprism-Summary-Cache-Hits", strconv.Itoa(c.CacheHits))
}
func (h *Handler) contextCapabilities() map[string]any {
	c := h.options.Context
	return map[string]any{"enabled": c.Enabled, "implementation": "gateway_summary", "native_encrypted_compaction": false, "phase": "before_generation", "auto_compact": c.AutoCompact, "summary_cache": c.SummaryCache, "summary_cache_max_entries": summaryCacheEntries, "summary_cache_ttl_seconds": int(c.ttl().Seconds()), "counting": "local_o200k_base_rendered"}
}
func (h *Handler) cacheCapabilities() map[string]any {
	return map[string]any{"affinity": h.options.Cache.Affinity, "prompt_cache_key": "scoped_account_affinity", "native_parameter_models": h.options.Cache.NativeModels, "native_hit_guarantee": false, "full_response_replay": false}
}
