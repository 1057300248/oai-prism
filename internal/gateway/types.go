// Package gateway implements the public OpenAI contract independently of the
// browser/Codex workbench. It never executes client tools or exposes admin APIs.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/oai-prism/oaiprism/internal/attachment"
	"github.com/oai-prism/oaiprism/internal/catalog"
	"net"
	"net/http"
	"strings"
	"time"
)

const MaxBody = 12 << 20
const MaxHistory = 16 << 20
const MaxOutput = 4 << 20
const MaxItems = 1024

// Options are operator-owned configuration, never request overrides.
type Options struct {
	Files            attachment.Options  `yaml:"files"`
	Media            MediaPolicy         `yaml:"media"`
	Catalog          catalog.Options     `yaml:"catalog"`
	CatalogEfforts   map[string][]string `yaml:"-"`
	CodexTools       bool                `yaml:"codex_tools"`
	Context          ContextPolicy       `yaml:"context"`
	Cache            PromptCachePolicy   `yaml:"prompt_cache"`
	Enabled          bool                `yaml:"enabled"`
	APIKeys          []string            `yaml:"-"`
	Models           map[string]string   `yaml:"-"`
	TenantHeader     string              `yaml:"tenant_header"`
	TrustedPeers     []string            `yaml:"trusted_peers"`
	ResponseStore    bool                `yaml:"response_store"`
	StorePath        string              `yaml:"store_path"`
	StoreKeyEnv      string              `yaml:"store_key_env"`
	UsagePolicy      string              `yaml:"usage_policy"`
	PromptTools      bool                `yaml:"prompt_tools"`
	StructuredOutput bool                `yaml:"structured_output"`
	InlineImages     bool                `yaml:"inline_images"`
	LocalOutputLimit bool                `yaml:"local_output_limit"`
	Timeout          time.Duration       `yaml:"timeout"`
	MaxConcurrent    int                 `yaml:"max_concurrent"`
}

func (o Options) Validate() error {
	if err := o.Files.Validate(); err != nil {
		return err
	}
	if err := o.Media.validate(); err != nil {
		return err
	}
	if o.Files.Enabled && (o.TenantHeader == "" || len(o.TrustedPeers) == 0) {
		return errors.New("files require a trusted tenant header and peer; shared channel keys are not user identities")
	}
	if o.Files.Path != "" && o.Files.Path == o.StorePath {
		return errors.New("files and response stores must use separate database paths")
	}
	if err := o.Catalog.Validate(); err != nil {
		return err
	}
	if o.CodexTools && !o.PromptTools {
		return errors.New("codex_tools requires prompt_tools")
	}
	if err := o.validateContextCache(); err != nil {
		return err
	}
	if !o.Enabled {
		return nil
	}
	validKey := false
	for _, key := range o.APIKeys {
		validKey = validKey || strings.TrimSpace(key) != ""
	}
	if !validKey {
		return errors.New("gateway requires at least one non-empty facade.api_keys entry")
	}
	if o.UsagePolicy != "" && o.UsagePolicy != "estimate" && o.UsagePolicy != "upstream_only" {
		return errors.New("gateway.usage_policy must be estimate or upstream_only")
	}
	if o.Timeout < 0 || o.MaxConcurrent < 0 {
		return errors.New("gateway timeout and concurrency must not be negative")
	}
	if o.ResponseStore {
		if strings.TrimSpace(o.TenantHeader) == "" || len(o.TrustedPeers) == 0 {
			return errors.New("response_store requires a trusted tenant_header and trusted_peers; a shared NewAPI channel key is not an end user")
		}
	}
	if o.TenantHeader != "" {
		h := strings.ToLower(o.TenantHeader)
		if h == "authorization" || h == "cookie" || h == "x-api-key" || h == "api-key" || strings.HasPrefix(h, "x-oaiprism-") || strings.HasPrefix(h, "x-prism-") {
			return errors.New("tenant_header conflicts with authentication or private control headers")
		}
		for _, c := range o.TenantHeader {
			if !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", c) {
				return errors.New("invalid tenant_header")
			}
		}
	}
	for _, cidr := range o.TrustedPeers {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid trusted peer CIDR: %w", err)
		}
	}
	if o.StorePath != "" && (!o.ResponseStore || o.StoreKeyEnv == "") {
		return errors.New("persistent store requires response_store and store_key_env (32-byte base64 AES key)")
	}
	return nil
}

type Content struct {
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}
type Item struct {
	Namespace string    `json:"namespace,omitempty"`
	Input     string    `json:"input,omitempty"`
	Phase     string    `json:"phase,omitempty"`
	Type      string    `json:"type"`
	ID        string    `json:"id,omitempty"`
	Role      string    `json:"role,omitempty"`
	Content   []Content `json:"content,omitempty"`
	Name      string    `json:"name,omitempty"`
	CallID    string    `json:"call_id,omitempty"`
	Arguments string    `json:"arguments,omitempty"`
	Output    string    `json:"output,omitempty"`
}
type Tool struct {
	Type                 string          `json:"type,omitempty"`
	Namespace            string          `json:"namespace,omitempty"`
	NamespaceDescription string          `json:"namespace_description,omitempty"`
	Format               json.RawMessage `json:"format,omitempty"`
	Name                 string          `json:"name"`
	Description          string          `json:"description,omitempty"`
	Parameters           json.RawMessage `json:"parameters"`
	Strict               bool            `json:"strict"`
}
type Request struct {
	FileIDs              []string
	HasMedia             bool
	MediaPolicy          MediaPolicy
	CodexTools           bool
	ReasoningSummary     string
	ClientMetadata       map[string]string
	ResolvedModel        string
	AllowedAccounts      []string
	DeclaredWindow       int
	PromptCacheKey       string
	PromptCacheRetention string
	PromptCacheOptions   map[string]json.RawMessage
	ScopedCacheKey       string
	CacheAffinity        bool
	NativeCacheForward   bool
	CompactThreshold     int
	InternalSummary      bool
	ContextReport        *ContextReport
	ContextUsage         *Usage
	Model                string
	Instructions         string
	Items                []Item
	Tools                []Tool
	ToolChoice           string // auto | none | required | a declared tool name
	Parallel             bool
	Stream               bool
	IncludeUsage         bool
	Store                bool
	PreviousID           string
	Metadata             map[string]string
	Effort               string
	Format               string // text | json_object | json_schema
	Schema               json.RawMessage
	SchemaName           string
	MaxTokens            int
	Stop                 []string
}

type Usage struct {
	CacheWrite *int
	Context    *ContextReport
	Input      int
	Output     int
	Cached     *int
	Reasoning  *int
	Source     string // upstream | estimated
}
type Result struct {
	ReasoningSummary string
	Text             string
	Calls            []Item
	Usage            *Usage
	Incomplete       bool
	Refusal          string
}
type Delta struct {
	Text  string
	Reset bool
}

// Engine must preserve ctx deadlines/cancellation. accepted is called after the
// upstream accepts the generation, before emitting anything. No replay after it.
type Engine interface {
	Run(ctx context.Context, req *Request, accepted func() error, emit func(Delta) error) (*Result, error)
}

type APIError struct {
	Context    *ContextReport
	Status     int
	Code       string
	Param      string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string { return e.Message }
func bad(param, message string) error {
	return &APIError{Status: 400, Code: "invalid_request_error", Param: param, Message: message}
}
func unsupported(param string) error {
	return &APIError{Status: 400, Code: "unsupported_parameter", Param: param, Message: "This parameter or value is not supported by this gateway transport."}
}
func missingResponse() error {
	return &APIError{Status: 404, Code: "response_not_found", Param: "previous_response_id", Message: "Response not found, expired, or not accessible to this caller."}
}
func publicError(err error) *APIError {
	var api *APIError
	if errors.As(err, &api) {
		return api
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &APIError{Status: 504, Code: "upstream_timeout", Message: "The upstream request exceeded its deadline."}
	}
	if errors.Is(err, context.Canceled) {
		return &APIError{Status: 499, Code: "request_cancelled", Message: "The request was cancelled."}
	}
	return &APIError{Status: 502, Code: "upstream_error", Message: "The upstream request failed. Refer to x-request-id when contacting the operator."}
}
func errorBody(api *APIError) map[string]any {
	typ := "server_error"
	if api.Status >= 400 && api.Status < 500 {
		typ = "invalid_request_error"
	}
	if api.Status == 429 {
		typ = "rate_limit_error"
	}
	var param any
	if api.Param != "" {
		param = api.Param
	}
	body := map[string]any{"error": map[string]any{"message": api.Message, "type": typ, "code": api.Code, "param": param}}
	if api.Context != nil {
		body["x_oaiprism_context"] = api.Context
	}
	return body
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
