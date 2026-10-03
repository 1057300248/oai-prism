package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type stream struct {
	w              http.ResponseWriter
	q              *Request
	responses      bool
	id             string
	created        int64
	mu             sync.Mutex
	opened         bool
	writeErr       error
	sequence       int
	terminal       bool
	beforeTerminal func(map[string]any) error
	text           strings.Builder
	textID         string
	stop           chan struct{}
	stopOnce       sync.Once
	wg             sync.WaitGroup
}

func newStream(w http.ResponseWriter, q *Request, responses bool, id string, created int64) *stream {
	return &stream{w: w, q: q, responses: responses, id: id, created: created, stop: make(chan struct{})}
}
func (s *stream) write(raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	rc := http.NewResponseController(s.w)
	if err := rc.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.writeErr = err
		return err
	}
	n, err := s.w.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = rc.Flush()
	}
	if err != nil {
		s.writeErr = err
	}
	return err
}
func (s *stream) open(ctx context.Context, cancel context.CancelFunc) error {
	if s.opened {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	s.w.Header().Set("Cache-Control", "no-cache, no-store, no-transform")
	s.w.Header().Set("X-Accel-Buffering", "no")
	s.w.Header().Add("Trailer", "X-Oaiprism-Usage-Source")
	for _, name := range []string{"X-Oaiprism-Context-Before", "X-Oaiprism-Context-After", "X-Oaiprism-Summary-Calls", "X-Oaiprism-Summary-Cache-Hits"} {
		s.w.Header().Add("Trailer", name)
	}
	s.opened = true
	if s.responses {
		for _, typ := range []string{"response.created", "response.in_progress"} {
			if err := s.event(typ, map[string]any{"response": responseJSON(s.q, s.id, s.created, "in_progress", nil, nil)}); err != nil {
				return err
			}
		}
	} else {
		if err := s.chat(map[string]any{"role": "assistant", "content": ""}, nil, nil, false); err != nil {
			return err
		}
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-ticker.C:
				if err := s.write([]byte(": keepalive\n\n")); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return nil
}
func (s *stream) event(typ string, body map[string]any) error {
	if s.terminal {
		return errors.New("semantic event after terminal response")
	}
	body["type"] = typ
	body["sequence_number"] = s.sequence
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	frame := append([]byte("event: "+typ+"\ndata: "), raw...)
	frame = append(frame, '\n', '\n')
	if err = s.write(frame); err != nil {
		return err
	}
	s.sequence++
	return nil
}
func (s *stream) chat(delta map[string]any, finish any, usage any, usageOnly bool) error {
	choices := []any{}
	if !usageOnly {
		choices = append(choices, map[string]any{"index": 0, "delta": delta, "finish_reason": finish, "logprobs": nil})
	}
	chunk := map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.q.Model, "choices": choices}
	if s.q.IncludeUsage {
		chunk["usage"] = usage
	}
	raw, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	return s.write(append(append([]byte("data: "), raw...), []byte("\n\n")...))
}
func messageJSON(id, text, status string) map[string]any {
	return map[string]any{"id": id, "type": "message", "status": status, "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}
}
func (s *stream) startText() error {
	if s.textID != "" {
		return nil
	}
	s.textID = "msg_" + uuid.NewString()
	if !s.responses {
		return nil
	}
	if err := s.event("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": s.textID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}}); err != nil {
		return err
	}
	return s.event("response.content_part.added", map[string]any{"item_id": s.textID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
}
func (s *stream) delta(d Delta) error {
	if s.terminal {
		return errors.New("delta after terminal response")
	}
	if d.Reset && s.text.Len() > 0 {
		return errors.New("upstream rewrote streamed text")
	}
	if s.text.Len()+len(d.Text) > MaxOutput {
		return errors.New("stream exceeds output size limit")
	}
	if d.Text == "" {
		return nil
	}
	if err := s.startText(); err != nil {
		return err
	}
	if s.responses {
		if err := s.event("response.output_text.delta", map[string]any{"item_id": s.textID, "output_index": 0, "content_index": 0, "delta": d.Text}); err != nil {
			return err
		}
	} else {
		if err := s.chat(map[string]any{"content": d.Text}, nil, nil, false); err != nil {
			return err
		}
	}
	s.text.WriteString(d.Text)
	return nil
}
func (s *stream) join()  { s.stopOnce.Do(func() { close(s.stop) }); s.wg.Wait() }
func (s *stream) close() { s.join(); _ = http.NewResponseController(s.w).SetWriteDeadline(time.Time{}) }

// finish emits validated, complete tool arguments as one delta. This is buffered
// adaptation, not fabricated incremental upstream token timing.
func (s *stream) finish(result *Result, usage *Usage) (map[string]any, error) {
	s.join()
	if !strings.HasPrefix(result.Text, s.text.String()) {
		return nil, errors.New("final text differs from the streamed prefix")
	}
	if err := s.delta(Delta{Text: result.Text[len(s.text.String()):]}); err != nil {
		return nil, err
	}
	output := []any{}
	if result.Text != "" || len(result.Calls) == 0 {
		if err := s.startText(); err != nil {
			return nil, err
		}
		item := messageJSON(s.textID, result.Text, "completed")
		output = append(output, item)
		if s.responses {
			if err := s.event("response.output_text.done", map[string]any{"item_id": s.textID, "output_index": 0, "content_index": 0, "text": result.Text}); err != nil {
				return nil, err
			}
			if err := s.event("response.content_part.done", map[string]any{"item_id": s.textID, "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": result.Text, "annotations": []any{}}}); err != nil {
				return nil, err
			}
			if err := s.event("response.output_item.done", map[string]any{"output_index": 0, "item": item}); err != nil {
				return nil, err
			}
		}
	}
	if item := reasoningOutput(s.q, result, "rs_"+uuid.NewString()); item != nil && s.responses {
		idx := len(output)
		output = append(output, item)
		if err := s.event("response.output_item.added", map[string]any{"output_index": idx, "item": item}); err != nil {
			return nil, err
		}
		if err := s.event("response.output_item.done", map[string]any{"output_index": idx, "item": item}); err != nil {
			return nil, err
		}
	}
	for index, call := range result.Calls {
		item := callJSON(call)
		outIndex := len(output)
		output = append(output, item)
		if s.responses {
			added := callJSON(call)
			field, eventPrefix, value := "arguments", "response.function_call_arguments", call.Arguments
			if call.Type == "custom_tool_call" {
				field, eventPrefix, value = "input", "response.custom_tool_call_input", call.Input
			}
			added[field] = ""
			added["status"] = "in_progress"
			if err := s.event("response.output_item.added", map[string]any{"output_index": outIndex, "item": added}); err != nil {
				return nil, err
			}
			if err := s.event(eventPrefix+".delta", map[string]any{"item_id": call.ID, "output_index": outIndex, "delta": value}); err != nil {
				return nil, err
			}
			if err := s.event(eventPrefix+".done", map[string]any{"item_id": call.ID, "output_index": outIndex, "name": call.Name, field: value}); err != nil {
				return nil, err
			}
			if err := s.event("response.output_item.done", map[string]any{"output_index": outIndex, "item": item}); err != nil {
				return nil, err
			}
		} else {
			if err := s.chat(map[string]any{"tool_calls": []any{map[string]any{"index": index, "id": call.CallID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": call.Arguments}}}}, nil, nil, false); err != nil {
				return nil, err
			}
		}
	}
	status := "completed"
	finish := "stop"
	if len(result.Calls) > 0 {
		finish = "tool_calls"
	}
	if result.Incomplete {
		status = "incomplete"
		finish = "length"
	}
	response := responseJSON(s.q, s.id, s.created, status, output, usage)
	if s.beforeTerminal != nil {
		if err := s.beforeTerminal(response); err != nil {
			return nil, err
		}
	}
	if s.responses {
		if err := s.event("response."+status, map[string]any{"response": response}); err != nil {
			return nil, err
		}
	} else {
		if err := s.chat(map[string]any{}, finish, nil, false); err != nil {
			return nil, err
		}
		if s.q.IncludeUsage {
			if err := s.chat(nil, nil, usageJSON(usage, false), true); err != nil {
				return nil, err
			}
		}
		if err := s.write([]byte("data: [DONE]\n\n")); err != nil {
			return nil, err
		}
	}
	s.terminal = true
	return response, nil
}
func (s *stream) fail(err error) error {
	s.join()
	if s.terminal {
		return nil
	}
	api := publicError(err)
	if s.responses {
		response := responseJSON(s.q, s.id, s.created, "failed", nil, nil)
		response["error"] = map[string]any{"code": api.Code, "message": api.Message}
		if api.Context != nil {
			response["x_oaiprism_context"] = api.Context
		}
		if err := s.event("response.failed", map[string]any{"response": response}); err != nil {
			return err
		}
	} else {
		raw, _ := json.Marshal(errorBody(api))
		if err := s.write(append(append([]byte("data: "), raw...), []byte("\n\n")...)); err != nil {
			return err
		}
	}
	s.terminal = true
	return nil // No [DONE] or finish_reason=stop after failure.
}
func callJSON(call Item) map[string]any {
	return toolCallJSON(call)
}
func outputJSON(result *Result) []any {
	output := []any{}
	if result.Text != "" || len(result.Calls) == 0 {
		output = append(output, messageJSON("msg_"+uuid.NewString(), result.Text, "completed"))
	}
	if result.ReasoningSummary != "" {
		output = append(output, map[string]any{"id": "rs_" + uuid.NewString(), "type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": result.ReasoningSummary}}})
	}
	for _, call := range result.Calls {
		output = append(output, callJSON(call))
	}
	return output
}
func responseJSON(q *Request, id string, created int64, status string, output []any, usage *Usage) map[string]any {
	if output == nil {
		output = []any{}
	}
	var previous, instructions, effort, incomplete, completed, maxTokens any
	if q.PreviousID != "" {
		previous = q.PreviousID
	}
	if q.Instructions != "" {
		instructions = q.Instructions
	}
	if q.Effort != "" {
		effort = q.Effort
	}
	if q.MaxTokens > 0 {
		maxTokens = q.MaxTokens
	}
	if status == "incomplete" {
		incomplete = map[string]any{"reason": "max_output_tokens"}
	}
	if status == "completed" {
		completed = time.Now().Unix()
	}
	tools := toolDefinitions(q)
	choice := selectedToolJSON(q)
	format := map[string]any{"type": q.Format}
	if q.Format == "json_schema" {
		format["name"] = q.SchemaName
		var schema any
		_ = json.Unmarshal(q.Schema, &schema)
		format["schema"] = schema
		format["strict"] = true
	}
	return map[string]any{"id": id, "object": "response", "x_oaiprism_context": q.ContextReport, "created_at": created, "completed_at": completed, "status": status, "model": q.Model, "output": output, "usage": usageJSON(usage, true), "error": nil, "incomplete_details": incomplete, "instructions": instructions, "previous_response_id": previous, "store": q.Store, "background": false, "metadata": q.Metadata, "tools": tools, "tool_choice": choice, "parallel_tool_calls": q.Parallel, "text": map[string]any{"format": format}, "reasoning": map[string]any{"effort": effort, "summary": nil}, "temperature": nil, "top_p": nil, "max_output_tokens": maxTokens, "truncation": "disabled"}
}
func chatJSON(q *Request, id string, created int64, result *Result, usage *Usage) map[string]any {
	var content any = result.Text
	message := map[string]any{"role": "assistant", "content": content}
	finish := "stop"
	if len(result.Calls) > 0 {
		finish = "tool_calls"
		calls := []any{}
		for _, call := range result.Calls {
			calls = append(calls, map[string]any{"id": call.CallID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": call.Arguments}})
		}
		message["tool_calls"] = calls
		if result.Text == "" {
			message["content"] = nil
		}
	}
	if result.Incomplete {
		finish = "length"
	}
	return map[string]any{"id": id, "object": "chat.completion", "x_oaiprism_context": q.ContextReport, "created": created, "model": q.Model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish, "logprobs": nil}}, "usage": usageJSON(usage, false)}
}
