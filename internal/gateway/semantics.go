package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/tiktoken-go/tokenizer"
)

// Contract validates completed outputs. Prompt adaptation is not constrained
// decoding and never runs functions on the gateway server.
type Contract struct {
	schemas map[string]*jsonschema.Schema
	output  *jsonschema.Schema
}

func Prepare(q *Request) (*Contract, error) {
	c := &Contract{schemas: map[string]*jsonschema.Schema{}}
	for _, t := range q.Tools {
		schema, err := compileSchema(t.Parameters)
		if err != nil {
			return nil, bad("tools.parameters", "Invalid, oversized, or externally-referencing JSON schema.")
		}
		c.schemas[t.Name] = schema
	}
	if q.Format == "json_schema" {
		schema, err := compileSchema(q.Schema)
		if err != nil {
			return nil, bad("text.format.schema", "Invalid, oversized, or externally-referencing JSON schema.")
		}
		c.output = schema
	}
	if len(q.Stop) > 0 && len(q.Tools) > 0 {
		return nil, bad("stop", "Stop strings cannot be combined with tool adaptation.")
	}
	return c, nil
}
func compileSchema(raw []byte) (*jsonschema.Schema, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return nil, errors.New("schema size")
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	nodes := 0
	var check func(any, int) error
	check = func(v any, depth int) error {
		nodes++
		if depth > 40 || nodes > 10000 {
			return errors.New("schema complexity")
		}
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if k == "$ref" || k == "$dynamicRef" {
					s, ok := v.(string)
					if !ok || !strings.HasPrefix(s, "#") {
						return errors.New("external reference")
					}
				}
				if k == "$id" {
					return errors.New("resource identity override")
				}
				if err := check(v, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, v := range x {
				if err := check(v, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err = check(value, 0); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(nil)
	const resource = "https://gateway.invalid/schema.json"
	if err = compiler.AddResource(resource, value); err != nil {
		return nil, err
	}
	return compiler.Compile(resource)
}

// PromptInstructions describes an explicitly emulated interface. It never
// represents the caller's history or tool outputs as higher-priority instructions.
func PromptInstructions(q *Request) string {
	if len(q.Tools) > 0 {
		definitions, _ := json.Marshal(q.Tools)
		return "\n\nThe caller's functions are executed ONLY by the caller, never in your workspace. Do not use internal shell/file tools to emulate them. Return exactly one JSON object with no markdown: {\"text\":\"answer or empty string\",\"tool_calls\":[{\"name\":\"declared function name\",\"arguments\":{}}]}. Arguments must satisfy the declared JSON schema. When no function is needed return an empty tool_calls array. Selection rule: " + q.ToolChoice + fmt.Sprintf(". Parallel calls allowed: %t. Declared functions: %s", q.Parallel, definitions)
	}
	if q.Format == "json_object" {
		return "\n\nReturn exactly one valid JSON object, without markdown or prose outside JSON."
	}
	if q.Format == "json_schema" {
		return "\n\nReturn exactly one JSON value satisfying this schema, without markdown or prose outside JSON: " + string(q.Schema)
	}
	return ""
}

func (c *Contract) Finalize(q *Request, result *Result) error {
	if result == nil {
		return errors.New("empty upstream result")
	}
	if len(result.Text) > MaxOutput {
		return errors.New("upstream output exceeds configured bound")
	}
	if len(q.Tools) > 0 && len(result.Calls) == 0 {
		// Strict envelope: ordinary prose is NOT relabelled as a successful tool call.
		fields, err := Object([]byte(strings.TrimSpace(result.Text)))
		if err != nil {
			return errors.New("upstream tool adapter did not return its declared envelope")
		}
		if keys(fields, "text tool_calls") != nil {
			return errors.New("invalid tool envelope properties")
		}
		var text string
		var calls []json.RawMessage
		if scalar(fields["text"], &text, "") != nil || scalar(fields["tool_calls"], &calls, "") != nil {
			return errors.New("invalid tool envelope")
		}
		if len(calls) > 128 {
			return errors.New("too many tool calls")
		}
		for _, raw := range calls {
			m, err := Object(raw)
			if err != nil || keys(m, "name arguments") != nil {
				return errors.New("invalid tool call")
			}
			var name string
			if scalar(m["name"], &name, "") != nil {
				return errors.New("missing function name")
			}
			arguments := m["arguments"]
			if len(arguments) == 0 || null(arguments) {
				return errors.New("missing function arguments")
			}
			result.Calls = append(result.Calls, Item{Type: "function_call", Name: name, Arguments: string(arguments)})
		}
		result.Text = text
	}
	if len(result.Calls) > 128 {
		return errors.New("too many tool calls")
	}
	if q.ToolChoice == "none" && len(result.Calls) > 0 {
		return errors.New("upstream violated tool_choice=none")
	}
	if q.ToolChoice == "required" && len(result.Calls) == 0 {
		return errors.New("upstream omitted required tool call")
	}
	if !q.Parallel && len(result.Calls) > 1 {
		return errors.New("upstream violated parallel_tool_calls=false")
	}
	for _, call := range result.Calls {
		schema, ok := c.schemas[call.Name]
		if !ok {
			return errors.New("upstream returned an undeclared function")
		}
		if q.ToolChoice != "auto" && q.ToolChoice != "required" && q.ToolChoice != call.Name {
			return errors.New("upstream violated named tool selection")
		}
		arguments, err := jsonschema.UnmarshalJSON(strings.NewReader(call.Arguments))
		if err != nil {
			return errors.New("upstream returned invalid function arguments")
		}
		if _, ok := arguments.(map[string]any); !ok {
			return errors.New("function arguments must be an object")
		}
		if err = schema.Validate(arguments); err != nil {
			return errors.New("upstream function arguments did not satisfy schema")
		}
	}
	if result.Incomplete {
		return nil
	}
	if q.Format != "text" {
		value, err := jsonschema.UnmarshalJSON(strings.NewReader(result.Text))
		if err != nil {
			return errors.New("upstream returned invalid structured JSON")
		}
		if q.Format == "json_object" {
			if _, ok := value.(map[string]any); !ok {
				return errors.New("expected a JSON object")
			}
		}
		if c.output != nil && c.output.Validate(value) != nil {
			return errors.New("upstream output did not satisfy JSON schema")
		}
	}
	for _, stop := range q.Stop {
		if i := strings.Index(result.Text, stop); i >= 0 {
			result.Text = result.Text[:i]
		}
	}
	if q.MaxTokens > 0 {
		if len(result.Calls) > 0 {
			total := 0
			for _, call := range result.Calls {
				n, err := CountTokens(call.Name + call.Arguments)
				if err != nil {
					return err
				}
				total += n
			}
			if total > q.MaxTokens {
				result.Calls = nil
				result.Text = ""
				result.Incomplete = true
				return nil
			}
		}
		clipped, hit, err := LimitText(result.Text, q.MaxTokens)
		if err != nil {
			return err
		}
		result.Text = clipped
		result.Incomplete = result.Incomplete || hit
	}
	return nil
}

// o200k_base counts are a disclosed local estimate, not authoritative Prism
// billing. Codec access is serialized because implementations may retain scratch.
var tokenState struct {
	once  sync.Once
	mu    sync.Mutex
	codec tokenizer.Codec
	err   error
}

func CountTokens(text string) (int, error) {
	tokenState.once.Do(func() { tokenState.codec, tokenState.err = tokenizer.Get(tokenizer.O200kBase) })
	if tokenState.err != nil {
		return 0, tokenState.err
	}
	tokenState.mu.Lock()
	defer tokenState.mu.Unlock()
	return tokenState.codec.Count(text)
}
func LimitText(text string, limit int) (string, bool, error) {
	if _, err := CountTokens(""); err != nil {
		return "", false, err
	}
	tokenState.mu.Lock()
	defer tokenState.mu.Unlock()
	ids, _, err := tokenState.codec.Encode(text)
	if err != nil {
		return "", false, err
	}
	if len(ids) <= limit {
		return text, false, nil
	}
	clipped, err := tokenState.codec.Decode(ids[:limit])
	if err != nil {
		return "", false, err
	}
	for !utf8.ValidString(clipped) && len(clipped) > 0 {
		clipped = clipped[:len(clipped)-1]
	}
	return clipped, true, nil
}
func NormalizeUsage(q *Request, result *Result, policy string) (*Usage, error) {
	u := result.Usage
	if u == nil || u.Source != "upstream" {
		if policy == "upstream_only" {
			return nil, errors.New("authoritative upstream usage unavailable")
		}
		var text strings.Builder
		text.WriteString(q.Instructions)
		text.WriteString(PromptInstructions(q))
		for _, it := range q.Items {
			text.WriteString(it.Role)
			text.WriteString(it.Name)
			text.WriteString(it.Arguments)
			text.WriteString(it.Output)
			for _, c := range it.Content {
				if c.Type == "input_image" {
					return nil, errors.New("image token usage cannot be estimated reliably; authoritative upstream usage required")
				}
				text.WriteString(c.Text)
			}
		}
		in, err := CountTokens(text.String())
		if err != nil {
			return nil, err
		}
		outText := result.Text
		for _, call := range result.Calls {
			outText += call.Name + call.Arguments
		}
		out, err := CountTokens(outText)
		if err != nil {
			return nil, err
		}
		u = &Usage{Input: in, Output: out, Source: "estimated"}
	}
	if u.Input < 0 || u.Output < 0 || u.Input > 1000000000 || u.Output > 1000000000 {
		return nil, errors.New("invalid upstream usage counts")
	}
	if u.Cached != nil && (*u.Cached < 0 || *u.Cached > u.Input) {
		return nil, errors.New("invalid cached token count")
	}
	if u.Reasoning != nil && (*u.Reasoning < 0 || *u.Reasoning > u.Output) {
		return nil, errors.New("invalid reasoning token count")
	}
	return u, nil
}
func usageJSON(u *Usage, responses bool) any {
	if u == nil {
		return nil
	}
	input, output, idetail, odetail := "prompt_tokens", "completion_tokens", "prompt_tokens_details", "completion_tokens_details"
	if responses {
		input, output, idetail, odetail = "input_tokens", "output_tokens", "input_tokens_details", "output_tokens_details"
	}
	m := map[string]any{input: u.Input, output: u.Output, "total_tokens": u.Input + u.Output, "x_oaiprism_source": u.Source}
	if u.Cached != nil {
		m[idetail] = map[string]any{"cached_tokens": *u.Cached}
	}
	if u.Reasoning != nil {
		m[odetail] = map[string]any{"reasoning_tokens": *u.Reasoning}
	}
	return m
}
