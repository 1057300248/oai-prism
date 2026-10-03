package gateway

import (
	"net/http"
	"sort"
	"strings"

	"github.com/oai-prism/oaiprism/internal/catalog"
)

// Parsing uses a request-owned snapshot. Refresh never mutates the shared Models
// map, and a candidate cannot become public without the operator publish list.
func (h *Handler) parseRequest(body []byte, responses bool) (*Request, error) {
	options := h.options
	options.Models = make(map[string]string, len(h.options.Models))
	for id, target := range h.options.Models {
		options.Models[id] = target
	}
	// Read one immutable source snapshot and aggregate it in a single pass.
	records := h.catalog.Records()
	declared := make(map[string]bool, len(h.options.Catalog.Publish))
	for _, id := range h.options.Catalog.Publish {
		declared[id] = true
		options.Models[id] = id
	}
	options.CatalogEfforts = map[string][]string{}
	options.Context.ModelWindows = map[string]int{}
	for id, w := range h.options.Context.ModelWindows {
		options.Context.ModelWindows[id] = w
	}
	for _, record := range records {
		if !declared[record.ID] {
			continue
		}
		options.Models[record.ID] = record.UpstreamID
		if !record.Stale && record.ContextWindow > 0 {
			window := options.Context.window(record.ID)
			if window == 0 || record.ContextWindow < window {
				options.Context.ModelWindows[record.ID] = record.ContextWindow
			}
		}
		options.CatalogEfforts[record.ID] = append(options.CatalogEfforts[record.ID], record.ReasoningEfforts...)
	}
	q, err := Parse(body, responses, options)
	if err != nil {
		return nil, err
	}
	q.CodexTools = options.CodexTools
	if declared[q.Model] {
		m, accounts, err := h.catalog.Select(q.Model, q.Effort)
		if err != nil {
			return nil, &APIError{Status: 503, Code: "model_capability_unavailable", Param: "model", Message: "No fresh account capability record satisfies this request."}
		}
		q.ResolvedModel = m.UpstreamID
		q.AllowedAccounts = accounts
		if q.Effort == "" {
			q.Effort = m.DefaultEffort
		}
		q.DeclaredWindow = m.ContextWindow
	}
	return q, nil
}
func validEffort(model, effort string, o Options) bool {
	if effort == "" {
		return true
	}
	if values, ok := o.CatalogEfforts[model]; ok {
		for _, v := range values {
			if v == effort {
				return true
			}
		}
		return false
	}
	switch effort {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return true
	}
	return false
}

// modelEntries contains no upstream account identifiers, tokens, URLs or source
// names. Different accounts may expose different efforts: routing checks them.
func (h *Handler) modelEntries() []map[string]any {
	byID := map[string]map[string]any{}
	for id := range h.options.Models {
		byID[id] = map[string]any{"id": id, "object": "model", "created": 0, "owned_by": "oaiprism", "x_oaiprism_evidence": "configured", "reasoning_efforts": []string{}, "context_window": nil, "input_modalities": []string{"text"}}
	}
	for _, id := range h.options.Catalog.Publish {
		byID[id] = map[string]any{"id": id, "object": "model", "created": 0, "owned_by": "oaiprism", "x_oaiprism_evidence": "unavailable", "x_oaiprism_stale": true, "reasoning_efforts": []string{}}
	}
	for _, r := range h.catalog.Records() {
		if !h.catalog.Published(r.ID) {
			continue
		}
		entry, ok := byID[r.ID]
		if !ok || entry["x_oaiprism_evidence"] == "configured" || entry["x_oaiprism_evidence"] == "unavailable" {
			entry = map[string]any{"id": r.ID, "object": "model", "created": 0, "owned_by": "oaiprism", "name": r.DisplayName, "x_oaiprism_evidence": "declared", "x_oaiprism_stale": r.Stale, "reasoning_efforts": []string{}, "context_window": nil, "input_modalities": []string{"text"}, "observed_at": r.ObservedAt}
			byID[r.ID] = entry
		}
		if !r.Stale {
			entry["x_oaiprism_stale"] = false
		}
		values := entry["reasoning_efforts"].([]string)
		for _, effort := range r.ReasoningEfforts {
			if r.Stale {
				continue
			}
			found := false
			for _, v := range values {
				found = found || v == effort
			}
			if !found {
				values = append(values, effort)
			}
		}
		entry["reasoning_efforts"] = values
		if r.ContextWindow > 0 {
			old, _ := entry["context_window"].(int)
			if old == 0 || r.ContextWindow < old {
				entry["context_window"] = r.ContextWindow
			}
		}
		// Gateway-emulated tools are not advertised as upstream-native tools.
		entry["x_oaiprism_tools"] = map[string]any{"function": h.options.PromptTools, "custom": h.options.CodexTools && h.options.PromptTools, "execution": "client", "implementation": "prompt_then_validate"}
	}
	ids := []string{}
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := []map[string]any{}
	for _, id := range ids {
		result = append(result, byID[id])
	}
	return result
}
func (h *Handler) serveCatalog(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/v1/models" && path != "/models" && path != "/v1/model-capabilities" && path != "/v1/codex/models" && !strings.HasPrefix(path, "/v1/models/") {
		return false
	}
	if r.Method != "GET" {
		h.method(w, "GET")
		return true
	}
	entries := h.modelEntries()
	if path != "/v1/model-capabilities" {
		fresh := []map[string]any{}
		for _, entry := range entries {
			if entry["x_oaiprism_stale"] != true {
				fresh = append(fresh, entry)
			}
		}
		entries = fresh
	}
	if strings.HasPrefix(path, "/v1/models/") {
		id := strings.TrimPrefix(path, "/v1/models/")
		for _, entry := range entries {
			if entry["id"] == id {
				writeJSON(w, 200, entry)
				return true
			}
		}
		h.fail(w, r, nil, &APIError{Status: 404, Code: "model_not_found", Param: "model", Message: "Unknown or unpublished model."})
		return true
	}
	if path == "/v1/codex/models" {
		result := []any{}
		// Codex export requires a fresh declared default effort and context window;
		// silently guessed metadata would re-enable unsupported client features.
		for _, entry := range entries {
			id := entry["id"].(string)
			model, _, err := h.catalog.Select(id, "")
			if err != nil || model.DefaultEffort == "" || model.ContextWindow <= 0 {
				continue
			}
			if efforts, ok := entry["reasoning_efforts"].([]string); ok && len(efforts) > 0 {
				model.ReasoningEfforts = efforts
			}
			result = append(result, CodexModel(model, h.options.CodexTools && h.options.PromptTools))
		}
		writeJSON(w, 200, map[string]any{"models": result})
		return true
	}
	object := "list"
	if path == "/v1/model-capabilities" {
		object = "gateway.model_capabilities"
	}
	writeJSON(w, 200, map[string]any{"object": object, "data": entries})
	return true
}

// CodexModel targets the public ModelInfo schema of Codex 0.160.0. No provider
// instructions, approval policy, MCP endpoints or unsafe tools are imported.
func CodexModel(m catalog.Model, tools bool) map[string]any {
	levels := []any{}
	for _, effort := range m.ReasoningEfforts {
		levels = append(levels, map[string]any{"effort": effort, "description": "Declared upstream effort; semantic effect requires live validation."})
	}
	patch := any(nil)
	shell := "disabled"
	if tools {
		patch = "freeform"
		shell = "unified_exec"
	}
	return map[string]any{
		"model_messages": map[string]any{"instructions_template": codexInstructions},
		"slug":           m.ID, "display_name": m.DisplayName, "description": "oai-prism declared capabilities; local tools execute in Codex, not on the gateway.",
		"default_reasoning_level": m.DefaultEffort, "supported_reasoning_levels": levels,
		"shell_type": shell, "visibility": "list", "supported_in_api": true, "priority": 0, "availability_nux": nil, "upgrade": nil,
		"support_verbosity": false, "default_verbosity": nil, "supports_reasoning_summary_parameter": false, "default_reasoning_summary": "none",
		"apply_patch_tool_type": patch, "truncation_policy": map[string]any{"mode": "bytes", "limit": 10000},
		"context_window": m.ContextWindow, "max_context_window": m.ContextWindow, "effective_context_window_percent": 90,
		"experimental_supported_tools": []any{}, "input_modalities": []string{"text"}, "supports_search_tool": false,
		"supports_experimental_context": false, "use_responses_lite": false, "supports_reasoning_effort_updates": false,
		"node_repl_disabled": true, "tool_mode": "direct", "include_skills_usage_instructions": true,
	}
}
