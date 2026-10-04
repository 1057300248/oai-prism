package prism

import (
	"encoding/json"
	"testing"
)

func TestUsageValidationPreservesDetails(t *testing.T) {
	for _, body := range []string{
		`{"input_tokens":10,"output_tokens":4,"input_tokens_details":{"cached_tokens":8},"output_tokens_details":{"reasoning_tokens":2}}`,
		`{"prompt_tokens":10,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":8},"completion_tokens_details":{"reasoning_tokens":2}}`,
	} {
		var u Usage
		if err := json.Unmarshal([]byte(body), &u); err != nil {
			t.Fatal(err)
		}
		if u.Invalid || u.TotalTokens != 14 || u.CachedTokens == nil || *u.CachedTokens != 8 || u.ReasoningTokens == nil || *u.ReasoningTokens != 2 {
			t.Fatalf("lost usage details %+v", u)
		}
	}
}
func TestUsageValidationRejectsInvalidNumbers(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, `{"input_tokens":10}`, `{"input_tokens":10.5,"output_tokens":4}`,
		`{"input_tokens":"10","output_tokens":4}`, `{"input_tokens":-1,"output_tokens":4}`,
		`{"input_tokens":1e100,"output_tokens":4}`, `{"input_tokens":10,"output_tokens":4,"total_tokens":13}`,
		`{"input_tokens":10,"output_tokens":4,"prompt_tokens":11}`,
		`{"input_tokens":10,"output_tokens":4,"input_tokens_details":{"cached_tokens":11}}`,
		`{"input_tokens":10,"output_tokens":4,"output_tokens_details":{"reasoning_tokens":5}}`,
	} {
		var u Usage
		if err := json.Unmarshal([]byte(body), &u); err != nil {
			t.Fatal(err)
		}
		if !u.Invalid {
			t.Errorf("invalid usage silently accepted: %s", body)
		}
	}
}
func TestUsageValidationDoesNotInventMissingDetails(t *testing.T) {
	var u Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":10,"output_tokens":4}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.Invalid || u.CachedTokens != nil || u.ReasoningTokens != nil {
		t.Fatalf("unknown details became zero: %+v", u)
	}
}
