package prism

import "encoding/json"

// UnmarshalJSON rejects fractional, negative, missing and conflicting counts.
// A malformed usage object remains explicitly invalid; it never becomes billed 0.
func (u *Usage) UnmarshalJSON(body []byte) error {
	*u = Usage{}
	var raw struct {
		Input        *int `json:"input_tokens"`
		Prompt       *int `json:"prompt_tokens"`
		Output       *int `json:"output_tokens"`
		Completion   *int `json:"completion_tokens"`
		Total        *int `json:"total_tokens"`
		InputDetails *struct {
			Cached *int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		PromptDetails *struct {
			Cached *int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		OutputDetails *struct {
			Reasoning *int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
		CompletionDetails *struct {
			Reasoning *int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	}
	if json.Unmarshal(body, &raw) != nil {
		u.Invalid = true
		return nil
	}
	if raw.Input != nil && raw.Prompt != nil && *raw.Input != *raw.Prompt {
		u.Invalid = true
	}
	if raw.Output != nil && raw.Completion != nil && *raw.Output != *raw.Completion {
		u.Invalid = true
	}
	if raw.Input == nil {
		raw.Input = raw.Prompt
	}
	if raw.Output == nil {
		raw.Output = raw.Completion
	}
	if raw.Input == nil || raw.Output == nil {
		u.Invalid = true
		return nil
	}
	u.InputTokens, u.OutputTokens = *raw.Input, *raw.Output
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.InputTokens > 1000000000 || u.OutputTokens > 1000000000 {
		u.Invalid = true
		return nil
	}
	u.TotalTokens = u.InputTokens + u.OutputTokens
	if raw.Total != nil && *raw.Total != u.TotalTokens {
		u.Invalid = true
	}
	if raw.InputDetails == nil {
		raw.InputDetails = raw.PromptDetails
	}
	if raw.OutputDetails == nil {
		raw.OutputDetails = raw.CompletionDetails
	}
	if raw.InputDetails != nil {
		u.CachedTokens = raw.InputDetails.Cached
	}
	if raw.OutputDetails != nil {
		u.ReasoningTokens = raw.OutputDetails.Reasoning
	}
	if u.CachedTokens != nil && (*u.CachedTokens < 0 || *u.CachedTokens > u.InputTokens) {
		u.Invalid = true
	}
	if u.ReasoningTokens != nil && (*u.ReasoningTokens < 0 || *u.ReasoningTokens > u.OutputTokens) {
		u.Invalid = true
	}
	return nil
}
func parseUsageMap(m map[string]any) *Usage {
	raw, err := json.Marshal(m)
	if err != nil {
		return &Usage{Invalid: true}
	}
	var usage Usage
	if json.Unmarshal(raw, &usage) != nil {
		usage.Invalid = true
	}
	return &usage
}
