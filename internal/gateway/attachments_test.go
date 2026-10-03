package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oai-prism/oaiprism/internal/attachment"
)

func fileHarness(t *testing.T, engine engineFunc) *Handler {
	return harness(t, engine, func(o *Options) {
		o.Files.Enabled = true
		o.TenantHeader = "X-Test-Tenant"
		o.TrustedPeers = []string{"127.0.0.1/32"}
		o.InlineImages = true
		o.Media.ImageReserve = 2048
		o.Media.PDFReserve = 8192
		o.Media.PDFModels = []string{"test-model"}
		o.ResponseStore = true
	})
}
func upload(t *testing.T, h http.Handler, name string, data []byte, tenant string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = writer.WriteField("purpose", "user_data")
	for k, v := range fields {
		_ = writer.WriteField(k, v)
	}
	_ = writer.Close()
	req := httptest.NewRequest("POST", "/v1/files", &body)
	req.RemoteAddr = "127.0.0.1:9000"
	req.Header.Set("Authorization", "Bearer key-a")
	req.Header.Set("X-Test-Tenant", tenant)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
func validPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestFilesLifecycleAndOwnership(t *testing.T) {
	h := fileHarness(t, okEngine)
	w := upload(t, h, "hello.txt", []byte("private"), "tenant-a", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := readMap(t, w)["id"].(string)
	for _, method := range []string{"GET", "DELETE"} {
		if bad := call(h, method, "/v1/files/"+id, "", "key-a", "tenant-b"); bad.Code != 404 {
			t.Fatal("cross-tenant file access", bad.Code)
		}
	}
	if got := readMap(t, call(h, "GET", "/v1/files", "", "key-a", "tenant-b"))["data"].([]any); len(got) != 0 {
		t.Fatal("leaked file list")
	}
	content := call(h, "GET", "/v1/files/"+id+"/content", "", "key-a", "tenant-a")
	if content.Code != 200 || content.Body.String() != "private" || content.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(content.Body.String())
	}
	if call(h, "DELETE", "/v1/files/"+id, "", "key-a", "tenant-a").Code != 200 {
		t.Fatal("delete failed")
	}
	if call(h, "GET", "/v1/files/"+id+"/content", "", "key-a", "tenant-a").Code != 404 {
		t.Fatal("deleted bytes available")
	}
}
func TestTextFileResolutionAndSnapshotRevocation(t *testing.T) {
	calls := 0
	h := fileHarness(t, func(ctx context.Context, q *Request, a func() error, e func(Delta) error) (*Result, error) {
		calls++
		found := false
		for _, i := range q.Items {
			for _, p := range i.Content {
				found = found || strings.Contains(p.Text, "hello-file-732")
			}
		}
		if !found {
			t.Fatal("file silently discarded")
		}
		return okEngine(ctx, q, a, e)
	})
	w := upload(t, h, "notes.md", []byte("hello-file-732"), "tenant", nil)
	id := readMap(t, w)["id"].(string)
	body, _ := json.Marshal(map[string]any{"model": "test-model", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_file", "file_id": id}}}}, "store": true})
	response := call(h, "POST", "/v1/responses", string(body), "key-a", "tenant")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	rid := readMap(t, response)["id"].(string)
	_ = call(h, "DELETE", "/v1/files/"+id, "", "key-a", "tenant")
	next, _ := json.Marshal(map[string]any{"model": "test-model", "input": "continue", "previous_response_id": rid, "store": false})
	if got := call(h, "POST", "/v1/responses", string(next), "key-a", "tenant"); got.Code != 404 {
		t.Fatal("deleted attachment used for continuation", got.Body.String())
	}
	if calls != 1 {
		t.Fatal("revoked file reached upstream")
	}
}
func TestFileExpansionLimitAndStrictMultipart(t *testing.T) {
	h := fileHarness(t, okEngine)
	for _, name := range []string{"../secret.txt", "dir\\secret.txt", "x.pdf", "x.exe"} {
		w := upload(t, h, name, []byte("not pdf or executable"), "t", nil)
		if w.Code != 400 {
			t.Fatal("invalid filename/type accepted", name, w.Code)
		}
	}
	w := upload(t, h, "x.txt", []byte("x"), "t", map[string]string{"purpose": "user_data"})
	if w.Code != 400 {
		t.Fatal("duplicate field", w.Code)
	}
	w = upload(t, h, "x.txt", []byte("x"), "t", map[string]string{"expires_after[anchor]": "created_at", "expires_after[seconds]": "2592000"})
	if w.Code != 400 {
		t.Fatal("retention policy ignored", w.Code)
	}
	w = upload(t, h, "x.txt", bytes.Repeat([]byte("x"), attachment.MaxFileBytes+1), "t", nil)
	if w.Code != 413 {
		t.Fatal("oversized upload", w.Code)
	}
	w = upload(t, h, "x.txt", bytes.Repeat([]byte("x"), 7<<20), "t", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	id := readMap(t, w)["id"].(string)
	input := []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_file", "file_id": id}, map[string]any{"type": "input_file", "file_id": id}}}}
	body, _ := json.Marshal(map[string]any{"model": "test-model", "input": input, "store": false})
	if got := call(h, "POST", "/v1/responses", string(body), "key-a", "t"); got.Code != 400 {
		t.Fatal("expansion budget bypass", got.Code)
	}
}
func TestImageFileIDToolResultAndAdmission(t *testing.T) {
	img := validPNG(t)
	observed := 0
	h := fileHarness(t, func(ctx context.Context, q *Request, a func() error, e func(Delta) error) (*Result, error) {
		n, err := RenderedTokens(q)
		if err != nil || n < 2048 || n > 4096 {
			t.Fatal("image admission reserve", n, err)
		}
		if !q.HasMedia {
			t.Fatal("missing media classification")
		}
		for _, it := range q.Items {
			for _, p := range it.Content {
				if p.Type == "input_image" {
					data, _, err := imageData(p.ImageURL)
					if err != nil || !bytes.Equal(img, data) {
						t.Fatal("image bytes lost")
					}
					observed++
				}
			}
		}
		return okEngine(ctx, q, a, e)
	})
	w := upload(t, h, "small.png", img, "t", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	id := readMap(t, w)["id"].(string)
	body, _ := json.Marshal(map[string]any{"model": "test-model", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "file_id": id}}}}, "store": false})
	if got := call(h, "POST", "/v1/responses", string(body), "key-a", "t"); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if observed != 1 {
		t.Fatal("image not received")
	}
}
func TestBinaryMediaDoesNotInventUsage(t *testing.T) {
	q := &Request{HasMedia: true, MediaPolicy: MediaPolicy{ImageReserve: 1000}, Items: []Item{{Type: "message", Role: "user", Content: []Content{{Type: "input_image", ImageURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(validPNG(t))}}}}}
	if _, err := NormalizeUsage(q, &Result{Text: "hello"}, "estimate"); err == nil {
		t.Fatal("admission estimate billed as image usage")
	}
	if _, err := NormalizeUsage(q, &Result{Text: "hello", Usage: &Usage{Input: 100, Output: 10, Source: "upstream"}}, "estimate"); err != nil {
		t.Fatal(err)
	}
}
func TestMediaCompactionProtectsBinaryTurn(t *testing.T) {
	items := []Item{}
	for i := 0; i < 8; i++ {
		parts := []Content{{Type: "input_text", Text: "history"}}
		if i == 3 {
			parts = append(parts, Content{Type: "input_image", ImageURL: "binary"})
		}
		items = append(items, Item{Type: "message", Role: "user", Content: parts}, Item{Type: "message", Role: "assistant", Content: []Content{{Type: "output_text", Text: "answer"}}})
	}
	_, history, _, end, err := partitionContext(items, 2)
	if err != nil || end > 6 {
		t.Fatal("binary turn sent to text summary", end, err)
	}
	for _, it := range history[:end] {
		for _, p := range it.Content {
			if p.Type == "input_image" {
				t.Fatal("binary in summary")
			}
		}
	}
}
func TestPDFPassthroughAndInlineFile(t *testing.T) {
	pdf := []byte("%PDF-1.4\n1 0 obj <<>> endobj\n%%EOF\n")
	h := fileHarness(t, func(ctx context.Context, q *Request, a func() error, e func(Delta) error) (*Result, error) {
		if len(q.Items) != 1 || q.Items[0].Content[0].Type != "input_file" {
			t.Fatal("PDF replaced or lost")
		}
		n, err := RenderedTokens(q)
		if err != nil || n < 8192 {
			t.Fatal(n, err)
		}
		return okEngine(ctx, q, a, e)
	})
	body, _ := json.Marshal(map[string]any{"model": "test-model", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_file", "filename": "a.pdf", "file_data": base64.StdEncoding.EncodeToString(pdf)}}}}, "store": false})
	if got := call(h, "POST", "/v1/responses", string(body), "key-a", "t"); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	h.options.Media.PDFModels = nil
	if got := call(h, "POST", "/v1/responses", string(body), "key-a", "t"); got.Code != 400 {
		t.Fatal("unverified PDF model accepted", got.Code)
	}
}
func TestFileListPaginationAndTypeRules(t *testing.T) {
	h := fileHarness(t, okEngine)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if upload(t, h, name, []byte(name), "t", nil).Code != 200 {
			t.Fatal("upload")
		}
	}
	first := readMap(t, call(h, "GET", "/v1/files?limit=1&order=asc", "", "key-a", "t"))
	if first["has_more"] != true {
		t.Fatal(first)
	}
	after := first["last_id"].(string)
	next := readMap(t, call(h, "GET", "/v1/files?limit=2&order=asc&after="+after, "", "key-a", "t"))
	if len(next["data"].([]any)) != 2 || next["has_more"] != false {
		t.Fatal(next)
	}
	for _, query := range []string{"limit=0", "limit=2&limit=3", "order=bad", "after=other", "purpose=assistants"} {
		if call(h, "GET", "/v1/files?"+query, "", "key-a", "t").Code != 400 {
			t.Fatal(query)
		}
	}
	if err := (Options{Files: attachment.Options{Enabled: true, TTL: time.Hour}}).Validate(); err == nil {
		t.Fatal("files enabled with shared-key-only ownership")
	}
}
