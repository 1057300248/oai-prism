package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// BridgePolicy is an operator-selected private-transport profile. It never comes
// from public metadata, never changes reasoning effort and never guesses tools.
type BridgePolicy struct {
	UploadMode           string             `yaml:"upload_mode"`
	MaxStartBytes        int                `yaml:"max_start_bytes"`
	InstructionPlacement string             `yaml:"instruction_placement"`
	AdditionalTools      bool               `yaml:"additional_tools"`
	Continuation         ContinuationPolicy `yaml:"continuation"`
}
type ContinuationPolicy struct {
	Enabled        bool          `yaml:"enabled"`
	VerifiedModels []string      `yaml:"verified_models"`
	TTL            time.Duration `yaml:"ttl"`
	SessionHeader  string        `yaml:"session_header"`
	ActionID       string        `yaml:"action_id"`
}

func (o Options) validateBridge() error {
	b := o.Bridge
	if b.UploadMode != "" && b.UploadMode != "multipart" && b.UploadMode != "raw" {
		return errors.New("bridge.upload_mode must be multipart or raw")
	}
	if b.InstructionPlacement != "" && b.InstructionPlacement != "system" && b.InstructionPlacement != "user_relay" {
		return errors.New("bridge.instruction_placement must be system or user_relay")
	}
	if b.MaxStartBytes != 0 && (b.MaxStartBytes < 4096 || b.MaxStartBytes > 16<<20) {
		return errors.New("bridge.max_start_bytes must be 0 or between 4096 and 16777216")
	}
	if b.AdditionalTools && (!o.CodexTools || !o.PromptTools) {
		return errors.New("bridge.additional_tools requires codex_tools and prompt_tools")
	}
	c := b.Continuation
	if c.TTL < 0 || c.TTL > time.Hour {
		return errors.New("continuation.ttl must be between 0 and 1h")
	}
	if c.Enabled && (!o.ResponseStore || o.TenantHeader == "" || len(o.TrustedPeers) == 0 || len(c.VerifiedModels) == 0) {
		return errors.New("continuation requires response_store, trusted tenant identity and verified_models")
	}
	if c.ActionID != "" && !regexp.MustCompile(`^[0-9a-f]{20,128}$`).MatchString(c.ActionID) {
		return errors.New("invalid continuation.action_id")
	}
	if c.SessionHeader != "" {
		if !regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$").MatchString(c.SessionHeader) {
			return errors.New("invalid continuation.session_header")
		}
		k := strings.ToLower(c.SessionHeader)
		if k == strings.ToLower(o.TenantHeader) || k == "authorization" || k == "cookie" || k == "x-api-key" || k == "api-key" || strings.HasPrefix(k, "x-oaiprism-") || strings.HasPrefix(k, "x-prism-") {
			return errors.New("session_header conflicts with identity or private controls")
		}
	}
	return nil
}

// The additional_tools compatibility profile treats the request as a complete
// declaration set. Configuration-update/stateful-delta dialects remain rejected.
func normalizeAdditionalTools(m map[string]json.RawMessage, responses bool, o Options) error {
	if !responses || !o.Bridge.AdditionalTools {
		return nil
	}
	raw := bytes.TrimSpace(m["input"])
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var items []json.RawMessage
	if err := scalar(raw, &items, "input"); err != nil {
		return err
	}
	if len(items) > MaxItems {
		return bad("input", "Too many input items.")
	}
	var tools []json.RawMessage
	if top, ok := m["tools"]; ok && !null(top) {
		if err := scalar(top, &tools, "tools"); err != nil {
			return err
		}
	}
	remaining := make([]json.RawMessage, 0, len(items))
	found := false
	for _, item := range items {
		fields, err := Object(item)
		if err != nil {
			return err
		}
		var kind string
		if value, ok := fields["type"]; ok {
			if err = scalar(value, &kind, "input.type"); err != nil {
				return err
			}
		}
		if kind != "additional_tools" {
			remaining = append(remaining, item)
			continue
		}
		found = true
		if err = keys(fields, "type tools"); err != nil {
			return err
		}
		var extra []json.RawMessage
		if err = scalar(fields["tools"], &extra, "additional_tools.tools"); err != nil {
			return err
		}
		tools = append(tools, extra...)
		if len(tools) > 128 {
			return bad("tools", "Combined tool declarations exceed 128 entries.")
		}
	}
	if !found {
		return nil
	}
	if len(remaining) == 0 {
		return bad("input", "additional_tools requires actual conversation input.")
	}
	// The normal function/custom/namespace parser still rejects duplicates and
	// malformed schemas after this normalization. Nothing is silently overwritten.
	var err error
	m["input"], err = json.Marshal(remaining)
	if err != nil {
		return err
	}
	m["tools"], err = json.Marshal(tools)
	return err
}

func RenderUpstreamInput(q *Request) []RenderMessage {
	return renderInputWindow(q, q.ContinuationOffset)
}

// This is an early lower-bound guard; StartResponse separately checks the exact
// JSON after private metadata and uploaded project paths have been assembled.
// Media bodies are uploaded separately and must not be counted as base64 text.
func CheckTransportBudget(q *Request) error {
	if q.Bridge.MaxStartBytes == 0 {
		return nil
	}
	rendered := RenderUpstreamInput(q)
	for i := range rendered {
		for j := range rendered[i].Content {
			p := &rendered[i].Content[j]
			if p.Type == "input_image" || p.Type == "input_file" {
				*p = Content{Type: "input_file"}
			}
		}
	}
	raw, err := json.Marshal(map[string]any{"input": rendered})
	if err != nil {
		return err
	}
	if len(raw) > q.Bridge.MaxStartBytes {
		return &APIError{Status: 400, Code: "context_length_exceeded", Param: "input", Stage: "transport_budget", Message: "The serialized request exceeds the configured upstream transport byte limit. Shorten or compact the input; no generation was started."}
	}
	return nil
}

func bridgeStatus(o Options) map[string]any {
	upload := o.Bridge.UploadMode
	if upload == "" {
		upload = "multipart"
	}
	placement := o.Bridge.InstructionPlacement
	if placement == "" {
		placement = "system"
	}
	return map[string]any{"object": "gateway.transport_status", "upload_mode": upload, "max_start_bytes": o.Bridge.MaxStartBytes, "instruction_placement": placement, "additional_tools": o.Bridge.AdditionalTools, "continuation_enabled": o.Bridge.Continuation.Enabled, "continuation_requires_store": true, "live_upstream_verified": false, "automatic_mode_fallback": false}
}
func writeGeneratedJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(value)
}
