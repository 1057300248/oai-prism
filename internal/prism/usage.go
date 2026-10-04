package prism

import "encoding/json"

type usageCacheDetails struct {
	Cached  *int `json:"cached_tokens"`
	Written *int `json:"cache_write_tokens"`
}
type usageOutputDetails struct {
	Reasoning *int `json:"reasoning_tokens"`
}

func usageCount(a, b *int, invalid *bool) *int {
	if a == nil {
		return b
	}
	if b != nil && *a != *b {
		*invalid = true
	}
	return a
}

// Unknown details stay nil. Read/write counts are provider reported, never
// derived from a local history/summary cache or inferred from token estimates.
func (u *Usage) UnmarshalJSON(body []byte) error {
	*u = Usage{}
	var raw struct {
		Input             *int                `json:"input_tokens"`
		Prompt            *int                `json:"prompt_tokens"`
		Output            *int                `json:"output_tokens"`
		Completion        *int                `json:"completion_tokens"`
		Total             *int                `json:"total_tokens"`
		InputDetails      *usageCacheDetails  `json:"input_tokens_details"`
		PromptDetails     *usageCacheDetails  `json:"prompt_tokens_details"`
		OutputDetails     *usageOutputDetails `json:"output_tokens_details"`
		CompletionDetails *usageOutputDetails `json:"completion_tokens_details"`
	}
	if json.Unmarshal(body, &raw) != nil {
		u.Invalid = true
		return nil
	}
	in := usageCount(raw.Input, raw.Prompt, &u.Invalid)
	out := usageCount(raw.Output, raw.Completion, &u.Invalid)
	if in == nil || out == nil {
		u.Invalid = true
		return nil
	}
	u.InputTokens, u.OutputTokens = *in, *out
	if *in < 0 || *out < 0 || *in > 1000000000 || *out > 1000000000 {
		u.Invalid = true
		return nil
	}
	u.TotalTokens = *in + *out
	if raw.Total != nil && *raw.Total != u.TotalTokens {
		u.Invalid = true
	}
	if raw.InputDetails == nil {
		raw.InputDetails = &usageCacheDetails{}
	}
	if raw.PromptDetails == nil {
		raw.PromptDetails = &usageCacheDetails{}
	}
	if raw.OutputDetails == nil {
		raw.OutputDetails = &usageOutputDetails{}
	}
	if raw.CompletionDetails == nil {
		raw.CompletionDetails = &usageOutputDetails{}
	}
	u.CachedTokens = usageCount(raw.InputDetails.Cached, raw.PromptDetails.Cached, &u.Invalid)
	u.CacheWriteTokens = usageCount(raw.InputDetails.Written, raw.PromptDetails.Written, &u.Invalid)
	u.ReasoningTokens = usageCount(raw.OutputDetails.Reasoning, raw.CompletionDetails.Reasoning, &u.Invalid)
	for _, n := range []*int{u.CachedTokens, u.CacheWriteTokens} {
		if n != nil && (*n < 0 || *n > *in) {
			u.Invalid = true
		}
	}
	if u.CachedTokens != nil && u.CacheWriteTokens != nil && *u.CachedTokens+*u.CacheWriteTokens > *in {
		u.Invalid = true
	}
	if u.ReasoningTokens != nil && (*u.ReasoningTokens < 0 || *u.ReasoningTokens > *out) {
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
