package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Handler struct {
	summaries *summaryCache
	options   Options
	engine    Engine
	store     *responseStore
	keys      [][32]byte
	peers     []*net.IPNet
	slots     chan struct{}
}

func New(o Options, engine Engine) (*Handler, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if engine == nil {
		return nil, errors.New("gateway engine is required")
	}
	h := &Handler{options: o, engine: engine, summaries: newSummaryCache()}
	for _, key := range o.APIKeys {
		if key = strings.TrimSpace(key); key != "" {
			h.keys = append(h.keys, sha256.Sum256([]byte(key)))
		}
	}
	for _, peer := range o.TrustedPeers {
		_, network, err := net.ParseCIDR(peer)
		if err != nil {
			return nil, err
		}
		h.peers = append(h.peers, network)
	}
	n := o.MaxConcurrent
	if n == 0 {
		n = 16
	}
	h.slots = make(chan struct{}, n)
	var err error
	h.store, err = newStore(o)
	if err != nil {
		return nil, err
	}
	return h, nil
}
func (h *Handler) Close() error { h.summaries.clear(); return h.store.Close() }
func (h *Handler) owner(r *http.Request) (string, error) {
	key := ""
	auths := 0
	for _, name := range []string{"Authorization", "X-Api-Key", "Api-Key"} {
		for _, value := range r.Header.Values(name) {
			auths++
			if name == "Authorization" {
				parts := strings.Fields(value)
				if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
					return "", &APIError{Status: 401, Code: "invalid_api_key", Message: "Invalid gateway API key."}
				}
				key = parts[1]
			} else {
				key = strings.TrimSpace(value)
			}
		}
	}
	if auths != 1 || len(key) > 4096 {
		return "", &APIError{Status: 401, Code: "invalid_api_key", Message: "Provide exactly one valid gateway API key."}
	}
	digest := sha256.Sum256([]byte(key))
	matched := 0
	for _, valid := range h.keys {
		matched |= subtle.ConstantTimeCompare(digest[:], valid[:])
	}
	if matched != 1 {
		return "", &APIError{Status: 401, Code: "invalid_api_key", Message: "Invalid gateway API key."}
	}
	tenant := ""
	if h.options.TenantHeader != "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return "", bad("tenant", "Cannot establish trusted proxy identity.")
		}
		ip := net.ParseIP(host)
		trusted := false
		for _, peer := range h.peers {
			trusted = trusted || peer.Contains(ip)
		}
		if !trusted {
			return "", &APIError{Status: 403, Code: "untrusted_proxy", Message: "Tenant identity is accepted only from configured trusted proxy peers."}
		}
		values := r.Header.Values(h.options.TenantHeader)
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" || len(values[0]) > 256 {
			return "", bad("tenant", "Missing or invalid trusted tenant identity.")
		}
		tenant = strings.TrimSpace(values[0])
	}
	owner := sha256.Sum256(append(append(digest[:], 0), []byte(tenant)...))
	return hex.EncodeToString(owner[:]), nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if w.Header().Get("X-Request-ID") == "" {
		w.Header().Set("X-Request-ID", "req_"+uuid.NewString())
	}
	owner, err := h.owner(r)
	if err != nil {
		h.fail(w, r, nil, err)
		return
	}
	path := r.URL.Path
	switch {
	case path == "/v1/models" || path == "/models":
		if r.Method != "GET" {
			h.method(w, "GET")
			return
		}
		names := []string{}
		for name := range h.options.Models {
			names = append(names, name)
		}
		sort.Strings(names)
		models := []any{}
		for _, name := range names {
			models = append(models, map[string]any{"id": name, "object": "model", "created": 0, "owned_by": "oaiprism"})
		}
		writeJSON(w, 200, map[string]any{"object": "list", "data": models})
		return
	case strings.HasPrefix(path, "/v1/models/"):
		if r.Method != "GET" {
			h.method(w, "GET")
			return
		}
		name := strings.TrimPrefix(path, "/v1/models/")
		if _, ok := h.options.Models[name]; !ok {
			h.fail(w, r, nil, &APIError{Status: 404, Code: "model_not_found", Param: "model", Message: "Unknown model."})
			return
		}
		writeJSON(w, 200, map[string]any{"id": name, "object": "model", "created": 0, "owned_by": "oaiprism"})
		return
	case path == "/v1/capabilities":
		if r.Method != "GET" {
			h.method(w, "GET")
			return
		}
		writeJSON(w, 200, map[string]any{"object": "gateway.capabilities", "context": h.contextCapabilities(), "prompt_cache": h.cacheCapabilities(), "chat_completions": true, "responses": true, "tools": map[string]any{"enabled": h.options.PromptTools, "implementation": "prompt_adaptation", "server_execution": false}, "structured_output": map[string]any{"enabled": h.options.StructuredOutput, "implementation": "prompt_then_validate"}, "inline_images": h.options.InlineImages, "remote_images": false, "response_store": h.options.ResponseStore, "response_store_ttl_seconds": int(storeTTL.Seconds()), "local_output_limit": h.options.LocalOutputLimit, "tokenizer": "o200k_base_local_estimate", "native_sampling_parameters": false})
		return
	case path == "/v1/responses/compact" || path == "/responses/compact":
		if r.Method != "POST" {
			h.method(w, "POST")
			return
		}
		h.contextEndpoint(w, r, owner, false)
		return
	case path == "/v1/responses/input_tokens":
		if r.Method != "POST" {
			h.method(w, "POST")
			return
		}
		h.contextEndpoint(w, r, owner, true)
		return
	case path == "/v1/chat/completions" || path == "/chat/completions":
		if r.Method != "POST" {
			h.method(w, "POST")
			return
		}
		h.generate(w, r, owner, false)
		return
	case path == "/v1/responses" || path == "/responses":
		if r.Method != "POST" {
			h.method(w, "POST")
			return
		}
		h.generate(w, r, owner, true)
		return
	case strings.HasPrefix(path, "/v1/responses/"):
		suffix := strings.TrimPrefix(path, "/v1/responses/")
		id, tail, _ := strings.Cut(suffix, "/")
		if len(id) > 128 || id == "" || !h.options.ResponseStore {
			h.fail(w, r, nil, missingResponse())
			return
		}
		if r.Method == "DELETE" && tail == "" {
			ok, err := h.store.Delete(r.Context(), id, owner)
			if err != nil {
				h.fail(w, r, nil, err)
				return
			}
			if !ok {
				h.fail(w, r, nil, missingResponse())
				return
			}
			writeJSON(w, 200, map[string]any{"id": id, "object": "response.deleted", "deleted": true})
			return
		}
		if r.Method != "GET" {
			h.method(w, "GET, DELETE")
			return
		}
		if tail != "" && tail != "input_items" {
			h.fail(w, r, nil, missingResponse())
			return
		}
		snapshot, ok, err := h.store.Get(r.Context(), id, owner)
		if err != nil {
			h.fail(w, r, nil, err)
			return
		}
		if !ok {
			h.fail(w, r, nil, missingResponse())
			return
		}
		if tail == "input_items" {
			page, err := inputItemsPage(snapshot, r.URL.Query())
			if err != nil {
				h.fail(w, r, nil, err)
				return
			}
			writeJSON(w, 200, page)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(snapshot.Response)
		return
	default:
		h.fail(w, r, nil, &APIError{Status: 404, Code: "not_found", Message: "This endpoint is not available in gateway mode."})
	}
}
func (h *Handler) method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	api := &APIError{Status: 405, Code: "method_not_allowed", Message: "Unsupported HTTP method."}
	writeJSON(w, 405, errorBody(api))
}
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, s *stream, err error) {
	if r.Context().Err() != nil {
		return
	}
	api := publicError(err)
	if s != nil && s.opened {
		_ = s.fail(api)
		return
	}
	if api.Status == 401 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="oaiprism"`)
	}
	if api.Status == 429 || api.Status == 503 {
		seconds := int(math.Ceil(api.RetryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		if seconds > 3600 {
			seconds = 3600
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	writeJSON(w, api.Status, errorBody(api))
}
func (h *Handler) generate(w http.ResponseWriter, r *http.Request, owner string, responses bool) {
	timeout := h.options.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	// Bound request-body allocation and schema compilation, not just inference.
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		h.fail(w, r, nil, &APIError{Status: 503, Code: "gateway_busy", Message: "Gateway concurrency capacity is exhausted."})
		return
	}
	for name := range r.Header {
		k := strings.ToLower(name)
		if strings.HasPrefix(k, "x-oaiprism-") || strings.HasPrefix(k, "x-prism-") || k == "x-local-workspace" || k == "openai-sentinel-token" || k == "x-openai-sentinel-token" {
			h.fail(w, r, nil, bad("headers", "Private upstream and local workspace overrides are disabled."))
			return
		}
	}
	if encoding := r.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		h.fail(w, r, nil, &APIError{Status: 415, Code: "unsupported_content_encoding", Message: "Send uncompressed JSON."})
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		media, _, err := mime.ParseMediaType(ct)
		if err != nil || media != "application/json" {
			h.fail(w, r, nil, &APIError{Status: 415, Code: "unsupported_media_type", Message: "Use application/json."})
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			err = &APIError{Status: 413, Code: "request_too_large", Message: "Request body exceeds the gateway limit."}
		}
		h.fail(w, r, nil, err)
		return
	}
	q, err := Parse(body, responses, h.options)
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
	for i := range q.Items {
		if q.Items[i].ID == "" {
			q.Items[i].ID = "item_" + uuid.NewString()
		}
	}
	raw, _ := json.Marshal(q.Items)
	if len(raw) > MaxHistory || len(q.Items) > MaxItems {
		h.fail(w, r, nil, bad("input", "Conversation history exceeds the gateway limit; provide a shorter explicit history."))
		return
	}
	if err = ValidateHistory(q.Items); err != nil {
		h.fail(w, r, nil, err)
		return
	}
	contract, err := Prepare(q)
	if err != nil {
		h.fail(w, r, nil, err)
		return
	}
	idPrefix := "chatcmpl-"
	if responses {
		idPrefix = "resp_"
	}
	id := idPrefix + uuid.NewString()
	created := time.Now().Unix()
	w.Header().Set("X-Oaiprism-Gateway", "1")
	w.Header().Set("X-Oaiprism-Usage-Policy", h.options.UsagePolicy)
	if q.MaxTokens > 0 {
		w.Header().Set("X-Oaiprism-Output-Limit", "local-visible-o200k_base")
	}
	buffered := len(q.Tools) > 0 || q.Format != "text" || q.MaxTokens > 0 || len(q.Stop) > 0
	if buffered {
		w.Header().Set("X-Oaiprism-Stream-Mode", "buffered-validation")
	} else {
		w.Header().Set("X-Oaiprism-Stream-Mode", "upstream-deltas")
	}
	var s *stream
	var accepted func() error
	var emit func(Delta) error
	if q.Stream {
		s = newStream(w, q, responses, id, created)
		defer s.close()
		accepted = func() error { return s.open(ctx, cancel) }
		emit = func(d Delta) error {
			if err := s.open(ctx, cancel); err != nil {
				return err
			}
			if buffered {
				return nil
			}
			return s.delta(d)
		}
	}
	h.bindPromptCache(q, owner)
	cacheMode := "disabled"
	if q.CacheAffinity {
		cacheMode = "scoped-affinity"
	}
	if q.NativeCacheForward {
		cacheMode = "native-parameters-forwarded"
	}
	w.Header().Set("X-Oaiprism-Prompt-Cache", cacheMode)
	if err := h.prepareContext(ctx, q, owner, false, accepted); err != nil {
		setContextHeaders(w, q)
		h.fail(w, r, s, withContextReport(q, err))
		return
	}
	setContextHeaders(w, q)
	for i := range q.Items {
		if q.Items[i].ID == "" {
			q.Items[i].ID = "item_" + uuid.NewString()
		}
	}
	result, err := h.engine.Run(ctx, q, accepted, emit)
	if s != nil {
		s.join()
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = contract.Finalize(q, result)
	}
	if err != nil {
		h.fail(w, r, s, withContextReport(q, err))
		return
	}
	usage, err := NormalizeUsage(q, result, h.options.UsagePolicy)
	if err != nil {
		h.fail(w, r, s, withContextReport(q, err))
		return
	}
	for i := range result.Calls {
		result.Calls[i].ID = "fc_" + uuid.NewString()
		result.Calls[i].CallID = "call_" + uuid.NewString()
		result.Calls[i].Type = "function_call"
	}
	history := append([]Item(nil), q.Items...)
	if result.Text != "" || len(result.Calls) == 0 {
		history = append(history, Item{Type: "message", Role: "assistant", Content: []Content{{Type: "output_text", Text: result.Text}}})
	}
	history = append(history, result.Calls...)
	persist := func(response map[string]any) error {
		if !q.Store {
			return nil
		}
		raw, err := json.Marshal(response)
		if err != nil {
			return err
		}
		return h.store.Put(ctx, id, owner, Snapshot{Response: raw, Items: history, InputItems: append([]Item(nil), q.Items...)})
	}
	w.Header().Set("X-Oaiprism-Usage-Source", usage.Source)
	if s != nil {
		if err = s.open(ctx, cancel); err != nil {
			return
		}
		s.join()
		s.beforeTerminal = persist
		_, err = s.finish(result, usage)
		if err != nil {
			_, _ = h.store.Delete(context.Background(), id, owner)
			h.fail(w, r, s, withContextReport(q, err))
		}
		return
	}
	var response map[string]any
	if responses {
		status := "completed"
		if result.Incomplete {
			status = "incomplete"
		}
		response = responseJSON(q, id, created, status, outputJSON(result), usage)
	} else {
		response = chatJSON(q, id, created, result, usage)
	}
	if err = persist(response); err != nil {
		h.fail(w, r, nil, err)
		return
	}
	writeJSON(w, 200, response)
}
