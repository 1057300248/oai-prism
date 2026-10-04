package facade

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/oai-prism/oaiprism/internal/account"
	"github.com/oai-prism/oaiprism/internal/config"
	"github.com/oai-prism/oaiprism/internal/creds"
	"github.com/oai-prism/oaiprism/internal/gateway"
	"github.com/oai-prism/oaiprism/internal/prism"
)

type gatewayEngine struct {
	cfg    *config.Config
	runner *Runner
}

func NewGatewayEngine(cfg *config.Config, runner *Runner) gateway.Engine {
	return &gatewayEngine{cfg: cfg, runner: runner}
}

func (g *gatewayEngine) Run(ctx context.Context, q *gateway.Request, accepted func() error, emit func(gateway.Delta) error) (*gateway.Result, error) {
	model, effort := q.Model, q.Effort
	if mapping, ok := g.cfg.Facade.Models[model]; ok && q.ResolvedModel == "" {
		if mapping.Model != "" {
			model = mapping.Model
		}
		if effort == "" {
			effort = mapping.ReasoningEffort
		}
	}
	if q.ResolvedModel != "" {
		model = q.ResolvedModel
	}
	input := gatewayInput(q)
	run := &RunRequest{AllowedAccounts: append([]string(nil), q.AllowedAccounts...), Input: input, Model: model, Effort: effort, API: "gateway", Isolated: true, Extra: q.NativeCacheFields(), GatewayStateful: q.StoredConversation, GatewayResume: q.UpstreamState, GatewayActionID: q.Bridge.Continuation.ActionID, GatewayMaxStartBytes: q.Bridge.MaxStartBytes}
	if q.UpstreamState != nil {
		run.AccountID = q.UpstreamState.AccountID
	}
	if q.CacheAffinity {
		run.StickyKey = q.ScopedCacheKey
	}
	if accepted != nil {
		run.OnAccepted = func(context.Context) error { return accepted() }
	}
	var callback func(Delta) error
	if emit != nil {
		callback = func(d Delta) error { return emit(gateway.Delta{Text: d.Text, Reset: d.Reset}) }
	}
	result, err := g.runner.Run(ctx, run, callback)
	if err != nil {
		stage := "account"
		var staged *gatewayStageError
		if errors.As(err, &staged) && staged.stage != "" {
			stage = staged.stage
		}
		var public *gateway.APIError
		if errors.As(err, &public) {
			copy := *public
			if copy.Stage == "" {
				copy.Stage = stage
			}
			return nil, &copy
		}
		var size *prism.StartSizeError
		if errors.As(err, &size) {
			return nil, &gateway.APIError{Status: 400, Code: "context_length_exceeded", Param: "input", Stage: "transport_budget", Message: "The serialized request exceeds the configured upstream byte limit; no generation was started."}
		}
		if errors.Is(err, context.Canceled) {
			return nil, &gateway.APIError{Status: 499, Code: "request_cancelled", Stage: stage, Message: "The request was cancelled."}
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrPollTimeout) {
			return nil, &gateway.APIError{Status: 504, Code: "upstream_timeout", Stage: stage, Message: "The upstream request exceeded its deadline."}
		}
		if errors.Is(err, account.ErrNoAccount) {
			return nil, &gateway.APIError{Status: 503, Code: "upstream_unavailable", Stage: "account", Message: "No eligible upstream account is available."}
		}
		var api *creds.APIError
		if errors.As(err, &api) && api.Status == 429 {
			return nil, &gateway.APIError{Status: 429, Code: "rate_limit_exceeded", Stage: stage, Message: "The upstream is rate limited. Retry later.", RetryAfter: api.RetryAfter}
		}
		return nil, &gateway.APIError{Status: 502, Code: "upstream_error", Stage: stage, Message: "The upstream request failed. Refer to x-request-id and x_oaiprism_stage."}
	}
	if result == nil {
		return nil, errors.New("upstream returned no result")
	}
	out := &gateway.Result{Text: result.Text, UpstreamState: result.GatewayState}
	if q.ReasoningSummary != "" && len(result.Reasoning) <= 1<<20 {
		out.ReasoningSummary = result.Reasoning
	}
	if result.Usage != nil && !result.UsageEstimated {
		u := result.Usage
		if u.Invalid {
			return nil, errors.New("invalid upstream usage")
		}
		if u.TotalTokens != u.InputTokens+u.OutputTokens {
			return nil, errors.New("inconsistent upstream usage")
		}
		out.Usage = &gateway.Usage{Input: u.InputTokens, Output: u.OutputTokens, Cached: u.CachedTokens, CacheWrite: u.CacheWriteTokens, Reasoning: u.ReasoningTokens, Source: "upstream"}
	}
	// Internal sandbox OutputItems/DeltaFiles describe work already performed in
	// Prism. They are not client function calls and must never be re-executed.
	return out, nil
}

func gatewayInput(q *gateway.Request) []prism.InputItem {
	rendered := gateway.RenderUpstreamInput(q)
	items := make([]prism.InputItem, 0, len(rendered))
	for _, message := range rendered {
		item := prism.InputItem{Type: message.Type, Role: message.Role}
		for _, part := range message.Content {
			item.Content = append(item.Content, prism.InputContent{Type: part.Type, Text: part.Text, ImageURL: part.ImageURL, Detail: part.Detail, Filename: part.Filename, GatewayFileData: part.FileData})
		}
		items = append(items, item)
	}
	return items
}

// preprocessGatewayImages uploads only prevalidated inline data and fails closed
// rather than silently sending an unreadable image after an upload error.
func preprocessGatewayImages(ctx context.Context, client *prism.Client, p prism.Principal, projectID string, items []prism.InputItem, modes ...string) ([]prism.InputItem, error) {
	mode := "multipart"
	if len(modes) > 0 {
		mode = modes[0]
	}
	output := make([]prism.InputItem, len(items))
	for i, item := range items {
		output[i] = item
		output[i].Content = append([]prism.InputContent(nil), item.Content...)
		for j, part := range item.Content {
			if part.Type == "input_file" {
				if projectID == "" || part.GatewayFileData == "" {
					return nil, errors.New("PDF requires an isolated project and resolved data")
				}
				data, err := base64.StdEncoding.Strict().DecodeString(part.GatewayFileData)
				if err != nil || len(data) > 8<<20 || !strings.HasPrefix(string(data), "%PDF-") {
					return nil, errors.New("invalid gateway PDF")
				}
				name := "gateway_pdf_" + randHex(12) + ".pdf"
				projectPath, err := client.UploadGatewayFile(ctx, p, prism.FileUpload{ProjectID: projectID, Path: name, Filename: name, Data: data}, mode)
				if err != nil {
					return nil, err
				}
				output[i].Content[j] = prism.InputContent{Type: "input_file", Filename: name, ProjectPath: projectPath}
				continue
			}
			if part.Type != "input_image" {
				continue
			}
			if !strings.HasPrefix(part.ImageURL, "data:image/") {
				return nil, errors.New("gateway images must be inline data")
			}
			if projectID == "" {
				return nil, errors.New("image upload requires isolated project")
			}
			data, ext, ok := extractImageData(ctx, part.ImageURL)
			if !ok {
				return nil, errors.New("invalid inline image")
			}
			name := "gateway_image_" + randHex(12) + ext
			projectPath, err := client.UploadGatewayFile(ctx, p, prism.FileUpload{ProjectID: projectID, Path: name, Filename: name, Data: data}, mode)
			if err != nil {
				return nil, err
			}
			output[i].Content[j] = prism.InputContent{Type: "input_file", Filename: name, ProjectPath: projectPath}
		}
	}
	return output, nil
}
