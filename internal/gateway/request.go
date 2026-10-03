package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var functionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Object rejects duplicate and case-conflicting keys, null, and trailing JSON.
func Object(body []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, bad("", "Expected one JSON object.")
	}
	out := map[string]json.RawMessage{}
	seen := map[string]bool{}
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return nil, bad("", "Invalid JSON object.")
		}
		name, ok := key.(string)
		if !ok {
			return nil, bad("", "Invalid property.")
		}
		folded := strings.ToLower(name)
		if seen[folded] {
			return nil, bad(name, "Duplicate or case-conflicting property.")
		}
		seen[folded] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, bad(name, "Invalid JSON value.")
		}
		out[name] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, bad("", "Invalid JSON object.")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, bad("", "Trailing data after request object.")
	}
	return out, nil
}
func keys(m map[string]json.RawMessage, allowed string) error {
	set := map[string]bool{}
	for _, k := range strings.Fields(allowed) {
		set[k] = true
	}
	for k := range m {
		if !set[k] {
			return unsupported(k)
		}
	}
	return nil
}
func null(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
func scalar(raw []byte, value any, param string) error {
	if null(raw) || json.Unmarshal(raw, value) != nil {
		return bad(param, "Invalid parameter type.")
	}
	return nil
}

func Parse(body []byte, responses bool, o Options) (*Request, error) {
	m, err := Object(body)
	if err != nil {
		return nil, err
	}
	allowed := "model stream store metadata user safety_identifier prompt_cache_key prompt_cache_retention prompt_cache_options tools tool_choice parallel_tool_calls"
	if responses {
		allowed += " input instructions previous_response_id reasoning text max_output_tokens background truncation include context_management"
	} else {
		allowed += " messages stream_options reasoning_effort response_format max_tokens max_completion_tokens stop n logprobs"
	}
	if err = keys(m, allowed); err != nil {
		return nil, err
	}
	q := &Request{Format: "text", ToolChoice: "auto", Parallel: true, Store: responses && o.ResponseStore, Metadata: map[string]string{}}
	for k, raw := range m {
		switch k {
		case "model":
			err = scalar(raw, &q.Model, k)
		case "instructions":
			if !null(raw) {
				err = scalar(raw, &q.Instructions, k)
			}
		case "previous_response_id":
			if !null(raw) {
				err = scalar(raw, &q.PreviousID, k)
			}
		case "stream":
			err = scalar(raw, &q.Stream, k)
		case "store":
			err = scalar(raw, &q.Store, k)
		case "parallel_tool_calls":
			err = scalar(raw, &q.Parallel, k)
		case "reasoning_effort":
			err = scalar(raw, &q.Effort, k)
		case "reasoning":
			if !null(raw) {
				var reason map[string]json.RawMessage
				reason, err = Object(raw)
				if err == nil {
					err = keys(reason, "effort summary")
				}
				if err == nil {
					if v, ok := reason["effort"]; ok {
						err = scalar(v, &q.Effort, k)
					}
				}
				if err == nil {
					if v, ok := reason["summary"]; ok && !null(v) {
						err = unsupported("reasoning.summary")
					}
				}
			}
		case "metadata":
			if !null(raw) {
				err = scalar(raw, &q.Metadata, k)
				if err == nil && len(q.Metadata) > 16 {
					err = bad(k, "At most 16 metadata entries are allowed.")
				}
				for name, value := range q.Metadata {
					if len(name) > 64 || len(value) > 512 {
						err = bad(k, "Metadata key/value exceeds 64/512 bytes.")
					}
				}
			}
		case "user", "safety_identifier":
			var value string
			err = scalar(raw, &value, k)
			if len(value) > 512 {
				err = bad(k, "Identity or cache label is too long.")
			} // Never used as authority or conversation identity.
		case "background", "logprobs":
			var value bool
			err = scalar(raw, &value, k)
			if value {
				err = unsupported(k)
			}
		case "n":
			var value int
			err = scalar(raw, &value, k)
			if value != 1 {
				err = unsupported(k)
			}
		case "include":
			var values []string
			err = scalar(raw, &values, k)
			if len(values) > 0 {
				err = unsupported(k)
			}
		case "truncation":
			var value string
			err = scalar(raw, &value, k)
			if value != "disabled" {
				err = unsupported(k)
			}
		case "max_tokens", "max_completion_tokens", "max_output_tokens":
			if !o.LocalOutputLimit {
				return nil, unsupported(k)
			}
			if q.MaxTokens != 0 {
				return nil, bad(k, "Do not combine output limit parameters.")
			}
			err = scalar(raw, &q.MaxTokens, k)
			if q.MaxTokens < 1 || q.MaxTokens > 1000000 {
				err = bad(k, "Output limit must be between 1 and 1000000.")
			}
		case "stop":
			if !null(raw) {
				var text string
				if json.Unmarshal(raw, &text) == nil {
					q.Stop = []string{text}
				} else {
					err = scalar(raw, &q.Stop, k)
				}
				if len(q.Stop) > 4 {
					err = bad(k, "At most four stop strings are allowed.")
				}
				for _, v := range q.Stop {
					if v == "" || len(v) > 1024 {
						err = bad(k, "Invalid stop string.")
					}
				}
			}
		case "stream_options":
			var sm map[string]json.RawMessage
			sm, err = Object(raw)
			if err == nil {
				err = keys(sm, "include_usage")
			}
			if err == nil {
				if v, ok := sm["include_usage"]; ok {
					err = scalar(v, &q.IncludeUsage, k)
				}
			}
		case "tools":
			if !null(raw) {
				q.Tools, err = parseTools(raw, responses)
			}
		case "tool_choice":
			var choice string
			if json.Unmarshal(raw, &choice) == nil {
				if choice != "none" && choice != "auto" && choice != "required" {
					err = bad(k, "Invalid tool choice.")
				} else {
					q.ToolChoice = choice
				}
			} else {
				var tm map[string]json.RawMessage
				tm, err = Object(raw)
				if err == nil {
					if responses {
						err = keys(tm, "type name")
					} else {
						err = keys(tm, "type function")
					}
				}
				if err == nil && string(tm["type"]) != `"function"` {
					err = unsupported(k)
				}
				if err == nil && !responses {
					tm, err = Object(tm["function"])
					if err == nil {
						err = keys(tm, "name")
					}
				}
				if err == nil {
					err = scalar(tm["name"], &q.ToolChoice, k)
				}
			}
		case "text", "response_format":
			var fm map[string]json.RawMessage
			fm, err = Object(raw)
			if err == nil && responses {
				err = keys(fm, "format")
				if err == nil {
					fm, err = Object(fm["format"])
				}
			}
			if err == nil {
				err = scalar(fm["type"], &q.Format, k)
			}
			if err == nil {
				switch q.Format {
				case "text":
					err = keys(fm, "type")
				case "json_object":
					err = keys(fm, "type")
				case "json_schema":
					if !responses {
						err = keys(fm, "type json_schema")
						if err == nil {
							fm, err = Object(fm["json_schema"])
						}
					}
					if err == nil {
						err = keys(fm, "type name schema strict description")
					}
					if err == nil {
						q.Schema = fm["schema"]
						err = scalar(fm["name"], &q.SchemaName, k)
						if !functionName.MatchString(q.SchemaName) {
							err = bad(k, "Invalid schema name.")
						}
					}
					if err == nil {
						if v, ok := fm["strict"]; ok {
							var strict bool
							err = scalar(v, &strict, k)
						}
					}
				default:
					err = unsupported(k)
				}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(q.Model) == "" || len(q.Model) > 256 {
		return nil, bad("model", "A configured model ID is required.")
	}
	if _, ok := o.Models[q.Model]; !ok {
		return nil, &APIError{Status: 404, Code: "model_not_found", Param: "model", Message: "This model is not configured on the gateway."}
	}
	if q.Effort != "" {
		switch q.Effort {
		case "none", "minimal", "low", "medium", "high", "xhigh":
		default:
			return nil, unsupported("reasoning.effort")
		}
	}
	if q.Store && (!responses || !o.ResponseStore) {
		return nil, unsupported("store")
	}
	if q.PreviousID != "" && (!o.ResponseStore || len(q.PreviousID) > 128) {
		return nil, unsupported("previous_response_id")
	}
	if q.IncludeUsage && !q.Stream {
		return nil, bad("stream_options", "stream_options requires stream=true.")
	}
	if q.Format != "text" && !o.StructuredOutput {
		return nil, unsupported("response_format")
	}
	if len(q.Tools) > 0 && !o.PromptTools {
		return nil, unsupported("tools")
	}
	if q.Format != "text" && len(q.Tools) > 0 {
		return nil, bad("tools", "Tool selection and structured final output cannot be combined in this transport.")
	}
	if q.ToolChoice != "auto" && q.ToolChoice != "none" {
		found := q.ToolChoice == "required" && len(q.Tools) > 0
		for _, t := range q.Tools {
			found = found || t.Name == q.ToolChoice
		}
		if !found {
			return nil, bad("tool_choice", "Tool choice must reference a declared function.")
		}
	}
	raw := m["messages"]
	if responses {
		raw = m["input"]
	}
	if responses && len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '"' {
		var text string
		if err = scalar(raw, &text, "input"); err != nil {
			return nil, err
		}
		q.Items = []Item{{Type: "message", Role: "user", Content: []Content{{Type: "input_text", Text: text}}}}
	} else {
		q.Items, err = parseItems(raw, responses, o)
	}
	if err != nil {
		return nil, err
	}
	if err := parseContextCache(m, q, o, responses); err != nil {
		return nil, err
	}
	return q, nil
}

func parseTools(raw []byte, responses bool) ([]Tool, error) {
	var list []json.RawMessage
	if err := scalar(raw, &list, "tools"); err != nil {
		return nil, err
	}
	if len(list) > 128 {
		return nil, bad("tools", "At most 128 functions are allowed.")
	}
	out := make([]Tool, 0, len(list))
	seen := map[string]bool{}
	for _, item := range list {
		m, err := Object(item)
		if err != nil {
			return nil, err
		}
		allowed := "type name description parameters strict"
		if !responses {
			allowed = "type function"
		}
		if err = keys(m, allowed); err != nil {
			return nil, err
		}
		if string(m["type"]) != `"function"` {
			return nil, unsupported("tools.type")
		}
		if !responses {
			m, err = Object(m["function"])
			if err != nil {
				return nil, err
			}
			if err = keys(m, "name description parameters strict"); err != nil {
				return nil, err
			}
		}
		var tool Tool
		if err = scalar(m["name"], &tool.Name, "tools.name"); err != nil {
			return nil, err
		}
		if !functionName.MatchString(tool.Name) || seen[tool.Name] {
			return nil, bad("tools.name", "Function names must be valid and unique.")
		}
		seen[tool.Name] = true
		if d, ok := m["description"]; ok {
			if err = scalar(d, &tool.Description, "tools.description"); err != nil {
				return nil, err
			}
		}
		if s, ok := m["strict"]; ok {
			if err = scalar(s, &tool.Strict, "tools.strict"); err != nil {
				return nil, err
			}
		}
		tool.Parameters = m["parameters"]
		if len(tool.Parameters) == 0 {
			tool.Parameters = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
		}
		out = append(out, tool)
	}
	return out, nil
}

func parseItems(raw []byte, responses bool, o Options) ([]Item, error) {
	var list []json.RawMessage
	if err := scalar(raw, &list, "input"); err != nil {
		return nil, err
	}
	if len(list) == 0 || len(list) > MaxItems {
		return nil, bad("input", "Expected 1..1024 input messages/items.")
	}
	out := make([]Item, 0, len(list))
	for index, b := range list {
		p := fmt.Sprintf("input[%d]", index)
		m, err := Object(b)
		if err != nil {
			return nil, err
		}
		typ := "message"
		if v, ok := m["type"]; ok {
			if err = scalar(v, &typ, p); err != nil {
				return nil, err
			}
		}
		if responses && (typ == "function_call" || typ == "function_call_output") {
			allowed := "type id call_id name arguments status"
			if typ == "function_call_output" {
				allowed = "type id call_id output"
			}
			if err = keys(m, allowed); err != nil {
				return nil, err
			}
			it := Item{Type: typ}
			if err = scalar(m["call_id"], &it.CallID, p); err != nil {
				return nil, err
			}
			if it.CallID == "" || len(it.CallID) > 128 {
				return nil, bad(p, "Invalid call_id.")
			}
			if typ == "function_call" {
				if err = scalar(m["name"], &it.Name, p); err != nil {
					return nil, err
				}
				if err = scalar(m["arguments"], &it.Arguments, p); err != nil {
					return nil, err
				}
				if !functionName.MatchString(it.Name) || !json.Valid([]byte(it.Arguments)) {
					return nil, bad(p, "Invalid function call.")
				}
			} else {
				if err = scalar(m["output"], &it.Output, p); err != nil {
					return nil, err
				}
			}
			out = append(out, it)
			continue
		}
		if typ != "message" {
			return nil, unsupported(p + ".type")
		}
		allowed := "role content tool_calls tool_call_id name"
		if responses {
			allowed = "type id role status content"
		}
		if err = keys(m, allowed); err != nil {
			return nil, err
		}
		var role string
		if err = scalar(m["role"], &role, p+".role"); err != nil {
			return nil, err
		}
		switch role {
		case "system", "developer", "user", "assistant":
		case "tool":
			if responses {
				return nil, unsupported(p + ".role")
			}
		default:
			return nil, unsupported(p + ".role")
		}
		if role == "tool" {
			var it Item
			it.Type = "function_call_output"
			if err = scalar(m["tool_call_id"], &it.CallID, p); err != nil {
				return nil, err
			}
			if err = scalar(m["content"], &it.Output, p); err != nil {
				return nil, err
			}
			out = append(out, it)
			continue
		}
		if content, ok := m["content"]; ok && !null(content) {
			parts, e := parseContent(content, role, o)
			if e != nil {
				return nil, e
			}
			out = append(out, Item{Type: "message", Role: role, Content: parts})
		} else if role != "assistant" || len(m["tool_calls"]) == 0 {
			return nil, bad(p, "Message content is required.")
		}
		if calls, ok := m["tool_calls"]; ok {
			if role != "assistant" {
				return nil, bad(p, "Only assistant messages can contain tool_calls.")
			}
			var tools []json.RawMessage
			if err = scalar(calls, &tools, p); err != nil {
				return nil, err
			}
			for _, call := range tools {
				cm, e := Object(call)
				if e != nil {
					return nil, e
				}
				if e = keys(cm, "id type function"); e != nil {
					return nil, e
				}
				if string(cm["type"]) != `"function"` {
					return nil, unsupported(p)
				}
				fm, e := Object(cm["function"])
				if e != nil {
					return nil, e
				}
				if e = keys(fm, "name arguments"); e != nil {
					return nil, e
				}
				it := Item{Type: "function_call"}
				if e = scalar(cm["id"], &it.CallID, p); e != nil {
					return nil, e
				}
				if e = scalar(fm["name"], &it.Name, p); e != nil {
					return nil, e
				}
				if e = scalar(fm["arguments"], &it.Arguments, p); e != nil {
					return nil, e
				}
				if !functionName.MatchString(it.Name) || !json.Valid([]byte(it.Arguments)) {
					return nil, bad(p, "Invalid function call.")
				}
				out = append(out, it)
			}
		}
	}
	if len(out) == 0 || len(out) > MaxItems {
		return nil, bad("input", "Invalid number of input items.")
	}
	return out, nil
}
func parseContent(raw []byte, role string, o Options) ([]Content, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []Content{{Type: "input_text", Text: text}}, nil
	}
	var blocks []json.RawMessage
	if err := scalar(raw, &blocks, "content"); err != nil {
		return nil, err
	}
	if len(blocks) == 0 || len(blocks) > 128 {
		return nil, bad("content", "Expected 1..128 content blocks.")
	}
	out := make([]Content, 0, len(blocks))
	for _, b := range blocks {
		m, err := Object(b)
		if err != nil {
			return nil, err
		}
		var typ string
		if err = scalar(m["type"], &typ, "content.type"); err != nil {
			return nil, err
		}
		switch typ {
		case "text", "input_text", "output_text":
			if err = keys(m, "type text annotations"); err != nil {
				return nil, err
			}
			if a, ok := m["annotations"]; ok {
				var annotations []any
				if err = scalar(a, &annotations, "annotations"); err != nil {
					return nil, err
				}
				if len(annotations) > 0 {
					return nil, unsupported("annotations")
				}
			}
			if err = scalar(m["text"], &text, "content.text"); err != nil {
				return nil, err
			}
			out = append(out, Content{Type: "input_text", Text: text})
		case "input_image", "image_url":
			if !o.InlineImages || role != "user" {
				return nil, unsupported("content.image")
			}
			if err = keys(m, "type image_url detail"); err != nil {
				return nil, err
			}
			var url, detail string
			detail = "auto"
			if typ == "image_url" {
				im, e := Object(m["image_url"])
				if e != nil {
					return nil, e
				}
				if e = keys(im, "url detail"); e != nil {
					return nil, e
				}
				if e = scalar(im["url"], &url, "image_url"); e != nil {
					return nil, e
				}
				if d, ok := im["detail"]; ok {
					if e = scalar(d, &detail, "detail"); e != nil {
						return nil, e
					}
				}
			} else {
				if err = scalar(m["image_url"], &url, "image_url"); err != nil {
					return nil, err
				}
				if d, ok := m["detail"]; ok {
					if err = scalar(d, &detail, "detail"); err != nil {
						return nil, err
					}
				}
			}
			if detail != "auto" && detail != "low" && detail != "high" {
				return nil, bad("detail", "Invalid image detail.")
			}
			if err = validateImage(url); err != nil {
				return nil, err
			}
			out = append(out, Content{Type: "input_image", ImageURL: url, Detail: detail})
		default:
			return nil, unsupported("content." + typ)
		}
	}
	return out, nil
}
func validateImage(url string) error {
	header, data, ok := strings.Cut(url, ",")
	if !ok {
		return bad("image_url", "Use an inline base64 PNG/JPEG/GIF/WebP image. Remote URLs are disabled to prevent SSRF.")
	}
	mime, ok := strings.CutPrefix(header, "data:")
	if !ok {
		return unsupported("image_url")
	}
	mime, ok = strings.CutSuffix(mime, ";base64")
	if !ok {
		return unsupported("image_url")
	}
	if mime != "image/png" && mime != "image/jpeg" && mime != "image/gif" && mime != "image/webp" {
		return unsupported("image_url")
	}
	if len(data) > 8<<20 {
		return bad("image_url", "Image exceeds 6 MiB decoded.")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(data)
	if err != nil || len(decoded) == 0 || len(decoded) > 6<<20 {
		return bad("image_url", "Invalid image data.")
	}
	if http.DetectContentType(decoded) != mime {
		return bad("image_url", "Image content does not match its declared media type.")
	}
	return nil
}

// ValidateHistory runs after previous_response_id expansion, so function outputs
// cannot fabricate call identities or overwrite completed calls.
func ValidateHistory(items []Item) error {
	pending := map[string]bool{}
	seen := map[string]bool{}
	for _, item := range items {
		switch item.Type {
		case "function_call":
			if item.CallID == "" || len(item.CallID) > 128 || seen[item.CallID] {
				return bad("input", "Duplicate or invalid function call ID.")
			}
			seen[item.CallID] = true
			pending[item.CallID] = true
		case "function_call_output":
			if !pending[item.CallID] {
				return bad("input", "Function output must reference a preceding unresolved call.")
			}
			delete(pending, item.CallID)
		}
	}
	if len(pending) > 0 {
		return bad("input", "Provide an output for every preceding function call.")
	}
	return nil
}
