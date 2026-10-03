package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oai-prism/oaiprism/internal/config"
)

func mediaTestPayload(t *testing.T, kind string) ([]byte, []byte) {
	t.Helper()
	var data []byte
	var part map[string]any
	if kind == "image" {
		var b bytes.Buffer
		if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		data = b.Bytes()
		part = map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)}
	} else {
		data = []byte("%PDF-1.4\n1 0 obj <<>> endobj\n%%EOF\n")
		part = map[string]any{"type": "input_file", "filename": "source.pdf", "file_data": base64.StdEncoding.EncodeToString(data)}
	}
	body, err := json.Marshal(map[string]any{"model": "instant", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Inspect the attachment"}, part}}}, "store": false})
	if err != nil {
		t.Fatal(err)
	}
	return body, data
}
func mediaTestConfig(c *config.Config) {
	c.Facade.Gateway.InlineImages = true
	c.Facade.Gateway.Files.Enabled = true
	c.Facade.Gateway.TenantHeader = "X-Test-Tenant"
	c.Facade.Gateway.TrustedPeers = []string{"127.0.0.1/32"}
	c.Facade.Gateway.Media.ImageReserve = 2048
	c.Facade.Gateway.Media.PDFReserve = 8192
	c.Facade.Gateway.Media.PDFModels = []string{"instant"}
}
func mediaRequest(ctx context.Context, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer channel-test-key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Tenant", "media-test-owner")
	return (&http.Client{Timeout: 5 * time.Second}).Do(req)
}
func TestMediaE2EExactBytesAndIsolatedProjectPaths(t *testing.T) {
	for _, kind := range []string{"image", "pdf"} {
		t.Run(kind, func(t *testing.T) {
			body, data := mediaTestPayload(t, kind)
			fake := &fakeUpstream{t: t}
			base := fake.handler()
			var mu sync.Mutex
			projects, names := []string{}, []string{}
			up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/project-files/upload" {
					base.ServeHTTP(w, r)
					return
				}
				if err := r.ParseMultipartForm(10 << 20); err != nil {
					t.Error(err)
					http.Error(w, "bad upload", 400)
					return
				}
				defer r.MultipartForm.RemoveAll()
				file, header, err := r.FormFile("file")
				if err != nil {
					t.Error(err)
					http.Error(w, "no file", 400)
					return
				}
				got, err := io.ReadAll(file)
				_ = file.Close()
				if err != nil || !bytes.Equal(got, data) {
					t.Error("attachment bytes changed", err)
				}
				if r.FormValue("project_id") == "" || r.FormValue("projectId") != r.FormValue("project_id") {
					t.Error("missing isolated project binding")
				}
				if header.Filename == "source.pdf" || !strings.HasPrefix(header.Filename, "gateway_") {
					t.Error("caller filename became remote path", header.Filename)
				}
				mu.Lock()
				projects = append(projects, r.FormValue("project_id"))
				names = append(names, header.Filename)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			})
			ts, _ := gatewayE2E(t, up, mediaTestConfig)
			for i := 0; i < 2; i++ {
				resp, err := mediaRequest(context.Background(), ts.URL+"/v1/responses", body)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 {
					t.Fatalf("%d %s", resp.StatusCode, raw)
				}
			}
			mu.Lock()
			if len(projects) != 2 || projects[0] == projects[1] || names[0] == names[1] {
				t.Error("attachments reused a mutable project/path", projects, names)
			}
			mu.Unlock()
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if len(fake.startBodies) != 2 {
				t.Fatal("generation start missing")
			}
			for i, start := range fake.startBodies {
				raw, _ := json.Marshal(start)
				if bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(data))) || bytes.Contains(raw, []byte("GatewayFileData")) {
					t.Fatal("binary uploaded and leaked again into start payload")
				}
				if !bytes.Contains(raw, []byte(names[i])) {
					t.Fatalf("uploaded attachment not referenced: %s", raw)
				}
			}
		})
	}
}
func TestMediaE2EFailedUploadNeverStartsGeneration(t *testing.T) {
	fake := &fakeUpstream{t: t}
	base := fake.handler()
	count := 0
	var mu sync.Mutex
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/project-files/upload" {
			_, _ = io.Copy(io.Discard, r.Body)
			mu.Lock()
			count++
			mu.Unlock()
			http.Error(w, "private upload failure", 400)
			return
		}
		base.ServeHTTP(w, r)
	})
	ts, _ := gatewayE2E(t, up, mediaTestConfig)
	body, _ := mediaTestPayload(t, "pdf")
	resp, err := mediaRequest(context.Background(), ts.URL+"/v1/responses", body)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 502 || bytes.Contains(raw, []byte("private upload failure")) {
		t.Fatalf("failed upload misreported/leaked: %d %s", resp.StatusCode, raw)
	}
	mu.Lock()
	defer mu.Unlock()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if count != 1 || len(fake.startBodies) != 0 {
		t.Fatal("failed upload replayed or generation started", count, len(fake.startBodies))
	}
}
func TestMediaE2ECancelledUploadStopsBeforeGeneration(t *testing.T) {
	fake := &fakeUpstream{t: t}
	base := fake.handler()
	started := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/project-files/upload" {
			_, _ = io.Copy(io.Discard, r.Body)
			started <- struct{}{}
			<-r.Context().Done()
			cancelled <- struct{}{}
			return
		}
		base.ServeHTTP(w, r)
	})
	ts, _ := gatewayE2E(t, up, mediaTestConfig)
	body, _ := mediaTestPayload(t, "image")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		resp, err := mediaRequest(ctx, ts.URL+"/v1/responses", body)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upload never started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled HTTP request succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client cancellation ignored")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream upload did not receive cancellation")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.startBodies) != 0 {
		t.Fatal("generation started after cancelled upload")
	}
}
