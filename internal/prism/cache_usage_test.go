package prism

import (
	"encoding/json"
	"testing"
)

func TestCacheWriteCountsAndAliasConflicts(t *testing.T) {
	for _, body := range []string{
		`{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":40}}`,
		`{"prompt_tokens":100,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":60,"cache_write_tokens":40}}`,
		`{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cached_tokens":60},"prompt_tokens_details":{"cache_write_tokens":40}}`,
	} {
		var u Usage
		if err := json.Unmarshal([]byte(body), &u); err != nil {
			t.Fatal(err)
		}
		if u.Invalid || u.CacheWriteTokens == nil || *u.CacheWriteTokens != 40 || u.CachedTokens == nil || *u.CachedTokens != 60 {
			t.Fatalf("usage detail lost %+v", u)
		}
	}
	for _, body := range []string{
		`{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cache_write_tokens":101}}`,
		`{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cache_write_tokens":-1}}`,
		`{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cache_write_tokens":0.5}}`,
		`{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cached_tokens":61,"cache_write_tokens":40}}`,
		`{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cache_write_tokens":40},"prompt_tokens_details":{"cache_write_tokens":30}}`,
		`{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cached_tokens":60},"prompt_tokens_details":{"cached_tokens":40}}`,
	} {
		var u Usage
		_ = json.Unmarshal([]byte(body), &u)
		if !u.Invalid {
			t.Error("invalid cache usage accepted", body)
		}
	}
	var u Usage
	_ = json.Unmarshal([]byte(`{"input_tokens":100,"output_tokens":4}`), &u)
	if u.CachedTokens != nil || u.CacheWriteTokens != nil {
		t.Fatal("missing counts synthesized as zero")
	}
}
func TestStatusParserPreservesCacheWrites(t *testing.T) {
	c := &Client{schema: testSchema()}
	for _, body := range []string{
		`{"status":"completed","text":"ok","usage":{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":40}}}`,
		`{"status":"completed","response":{"status":"success","payload":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":100,"output_tokens":4,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":40}}}}}`,
	} {
		result, err := c.ParseStatusPayload([]byte(body), "req", "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Usage == nil || result.Usage.Invalid || result.Usage.CacheWriteTokens == nil || *result.Usage.CacheWriteTokens != 40 {
			t.Fatal("cache writes lost in parser", result.Usage)
		}
	}
}
