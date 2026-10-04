package prism

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/oai-prism/oaiprism/internal/creds"
)

// StartSizeError means no generation POST was sent. It does not mean the model's
// context window was measured, nor does it authorize splitting into paid turns.
type StartSizeError struct{ Actual, Limit int }

func (e *StartSizeError) Error() string {
	return fmt.Sprintf("serialized upstream start is %d bytes, limit %d", e.Actual, e.Limit)
}
func checkStartSize(payload map[string]any, limit int) error {
	if limit <= 0 {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(raw) > limit {
		return &StartSizeError{len(raw), limit}
	}
	return nil
}
func boundedResponse(resp *http.Response, max int64) ([]byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, errors.New("upstream response exceeded configured bound")
	}
	return body, nil
}

// UploadGatewayFile preserves the workbench multipart implementation by default.
// The optional raw profile implements the observed browser binary-body contract.
// Neither profile retries or switches wire format after an uncertain upload.
func (c *Client) UploadGatewayFile(ctx context.Context, p Principal, up FileUpload, mode string) (string, error) {
	if mode == "" || mode == "multipart" {
		_, err := c.UploadFile(ctx, p, up)
		return up.Path, err
	}
	if mode != "raw" {
		return "", errors.New("unsupported gateway upload mode")
	}
	if up.ProjectID == "" || len(up.Data) == 0 || len(up.Data) > 8<<20 || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(up.Filename) {
		return "", errors.New("invalid gateway upload")
	}
	mime := http.DetectContentType(up.Data)
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "application/pdf":
	default:
		return "", errors.New("unsupported raw upload media")
	}
	headers := c.buildHeaders(p, mime, "application/json")
	headers["x-prism-file-id"] = newUUID()
	headers["x-prism-file-name"] = up.Filename
	headers["x-prism-file-size"] = strconv.Itoa(len(up.Data))
	headers["x-prism-project-id"] = up.ProjectID
	headers["x-prism-require-project-edit-access"] = "true"
	resp, err := c.Do(ctx, p, http.MethodPost, PathProjectFilesUpload, headerFromMap(headers), up.Data, nil)
	if err != nil {
		return "", err
	}
	raw, err := boundedResponse(resp, 1<<20)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &creds.APIError{Op: "upload", Status: resp.StatusCode, Body: "raw project upload failed"}
	}
	expected := "/prism-uploads/" + up.Filename
	var result map[string]json.RawMessage
	if len(strings.TrimSpace(string(raw))) > 0 {
		if json.Unmarshal(raw, &result) != nil {
			return "", errors.New("raw upload returned non-JSON success")
		}
		for _, key := range []string{"error", "error_code"} {
			if v := result[key]; len(v) > 0 && string(v) != "null" && string(v) != `""` {
				return "", errors.New("raw upload returned an error envelope")
			}
		}
		for _, key := range []string{"projectPath", "project_path"} {
			if v, ok := result[key]; ok {
				var path string
				if json.Unmarshal(v, &path) != nil || path != expected {
					return "", errors.New("raw upload returned an unexpected project path")
				}
			}
		}
	}
	return expected, nil
}

var actionPattern = regexp.MustCompile(`createServerReference\s*\)?\s*\(\s*["']([0-9a-f]{20,128})["'][^()\r\n]{0,240}?["']createProjectConversation["']\s*\)`)
var scriptPattern = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc=["']([^"']+)["']`)
var storedCIDPattern = regexp.MustCompile(`^cdx[0-9]+_[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)
var actionIDPattern = regexp.MustCompile(`^[0-9a-f]{20,128}$`)

func actionIDs(source string) []string {
	found := []string{}
	seen := map[string]bool{}
	for _, m := range actionPattern.FindAllStringSubmatch(source, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			found = append(found, m[1])
		}
	}
	return found
}
func (c *Client) readTransportPage(ctx context.Context, p Principal, path string, limit int64) ([]byte, error) {
	resp, err := c.Do(ctx, p, http.MethodGet, path, headerFromMap(c.buildHeaders(p, "", "text/html,application/javascript")), nil, nil)
	if err != nil {
		return nil, err
	}
	raw, err := boundedResponse(resp, limit)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, &creds.APIError{Op: "conversation_discovery", Status: resp.StatusCode, Body: "conversation metadata unavailable"}
	}
	return raw, nil
}
func (c *Client) discoverConversationAction(ctx context.Context, p Principal, projectID string) (string, error) {
	page, err := c.readTransportPage(ctx, p, "/?u="+url.QueryEscape(projectID), 512<<10)
	if err != nil {
		return "", err
	}
	ids := map[string]bool{}
	for _, id := range actionIDs(string(page)) {
		ids[id] = true
	}
	origin, _ := url.Parse(c.up.Origin)
	var base *url.URL
	if c.base != nil {
		base = c.base.BaseURL
	}
	visited := map[string]bool{}
	total := len(page)
	for _, match := range scriptPattern.FindAllStringSubmatch(string(page), -1) {
		u, err := url.Parse(html.UnescapeString(match[1]))
		if err != nil || u.User != nil || u.Fragment != "" {
			continue
		}
		if u.IsAbs() || u.Host != "" {
			if u.Scheme != "https" && u.Scheme != "http" {
				continue
			}
			if (origin == nil || u.Host != origin.Host) && (base == nil || u.Host != base.Host) {
				continue
			}
		}
		if !strings.HasPrefix(u.Path, "/_next/static/") || !strings.HasSuffix(u.Path, ".js") {
			continue
		}
		path := u.EscapedPath()
		if u.RawQuery != "" {
			path += "?" + u.RawQuery
		}
		if visited[path] {
			continue
		}
		if len(visited) >= 16 || total >= 4<<20 {
			return "", errors.New("conversation discovery budget exceeded")
		}
		visited[path] = true
		raw, err := c.readTransportPage(ctx, p, path, 512<<10)
		if err != nil {
			return "", err
		}
		total += len(raw)
		if total > 4<<20 {
			return "", errors.New("conversation discovery budget exceeded")
		}
		for _, id := range actionIDs(string(raw)) {
			ids[id] = true
		}
	}
	if len(ids) != 1 {
		return "", errors.New("conversation creation action missing or ambiguous; refresh the verified transport profile")
	}
	for id := range ids {
		return id, nil
	}
	return "", errors.New("missing conversation action")
}

// CreateStoredConversation invokes only the named same-origin server action.
// Missing/changed action IDs fail, rather than inventing a continuable UUID.
func (c *Client) CreateStoredConversation(ctx context.Context, p Principal, projectID, actionID string) (string, error) {
	if projectID == "" || len(projectID) > 256 {
		return "", errors.New("invalid isolated project")
	}
	if actionID == "" {
		var err error
		actionID, err = c.discoverConversationAction(ctx, p, projectID)
		if err != nil {
			return "", err
		}
	}
	if !actionIDPattern.MatchString(actionID) {
		return "", errors.New("invalid observed conversation action")
	}
	body, _ := json.Marshal([]string{projectID})
	headers := c.buildHeaders(p, "text/plain;charset=UTF-8", "text/x-component")
	headers["Next-Action"] = actionID
	resp, err := c.Do(ctx, p, http.MethodPost, "/?u="+url.QueryEscape(projectID), headerFromMap(headers), body, nil)
	if err != nil {
		return "", err
	}
	raw, err := boundedResponse(resp, 64<<10)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", &creds.APIError{Op: "conversation_create", Status: resp.StatusCode, Body: "stored conversation creation failed"}
	}
	ids := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		_, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "E{") {
			return "", errors.New("stored conversation action returned an error")
		}
		var id string
		if json.Unmarshal([]byte(value), &id) == nil && storedCIDPattern.MatchString(id) {
			ids[id] = true
		}
	}
	if len(ids) != 1 {
		return "", errors.New("stored conversation action returned no unique conversation ID")
	}
	for id := range ids {
		return id, nil
	}
	return "", errors.New("stored conversation unavailable")
}

func (c *Client) ProbeStoredSandbox(ctx context.Context, p Principal, sb *Sandbox) error {
	if !sb.Usable() {
		return errors.New("stored sandbox unavailable")
	}
	u, err := url.Parse(sb.URL)
	if err != nil || u.User != nil {
		return errors.New("invalid stored sandbox URL")
	}
	origin, _ := url.Parse(c.up.Origin)
	var base *url.URL
	if c.base != nil {
		base = c.base.BaseURL
	}
	if u.Host != "" && (origin == nil || u.Host != origin.Host) && (base == nil || u.Host != base.Host) {
		return errors.New("stored sandbox origin mismatch")
	}
	path := strings.TrimSuffix(u.Path, "/")
	if path != "/s/sandboxes/proxy" && path != "/sandboxes/proxy" {
		return errors.New("stored sandbox path mismatch")
	}
	resp, err := c.Do(ctx, p, http.MethodGet, path+"/heartbeat", headerFromMap(mergeSandboxHeaders(c.buildHeaders(p, "", "*/*"), sb.Token)), nil, nil)
	if err != nil {
		return err
	}
	_, err = boundedResponse(resp, 4096)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return &creds.APIError{Op: "sandbox_resume", Status: resp.StatusCode, Body: "stored sandbox expired or unavailable"}
	}
	return nil
}
func mergeSandboxHeaders(headers map[string]string, token string) map[string]string {
	headers["X-Crixet-Sandbox-Token"] = token
	return headers
}

// Nested payload snapshots occur on completed turns. Both known camel/snake
// spellings are supported without recursively trusting arbitrary user content.
func enrichContinuationStatus(st *StatusResponse, raw []byte) {
	if st == nil {
		return
	}
	var env struct {
		Snapshot json.RawMessage `json:"codex_listen_snapshot"`
		Camel    json.RawMessage `json:"codexListenSnapshot"`
		Response *struct {
			Payload *struct {
				Conversation string          `json:"conversationId"`
				Snapshot     json.RawMessage `json:"codex_listen_snapshot"`
				Camel        json.RawMessage `json:"codexListenSnapshot"`
			} `json:"payload"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return
	}
	candidates := []json.RawMessage{env.Snapshot, env.Camel}
	if env.Response != nil && env.Response.Payload != nil {
		p := env.Response.Payload
		candidates = append(candidates, p.Snapshot, p.Camel)
		if p.Conversation != "" {
			st.ConversationID = p.Conversation
		}
	}
	for _, v := range candidates {
		if len(v) == 0 || string(v) == "null" || len(v) > 64<<10 {
			continue
		}
		var text string
		if json.Unmarshal(v, &text) == nil {
			v = json.RawMessage(text)
		}
		var obj map[string]json.RawMessage
		if len(v) <= 64<<10 && json.Unmarshal(v, &obj) == nil && obj != nil {
			st.ListenSnapshot = append(json.RawMessage(nil), v...)
		}
	}
}
