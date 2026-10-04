package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oai-prism/oaiprism/internal/catalog"
)

func TestCatalogParseHasBoundedSnapshotCopies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	models := make([]catalog.Model, 100)
	published := make([]string, len(models))
	for i := range models {
		id := fmt.Sprintf("model-%03d", i)
		published[i] = id
		models[i] = catalog.Model{ID: id, UpstreamID: id, ReasoningEfforts: []string{"medium", "high"}, DefaultEffort: "medium", ContextWindow: 65536}
	}
	raw, _ := json.Marshal(catalog.Snapshot{ObservedAt: time.Now(), Models: models})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	h := harness(t, func(context.Context, *Request, func() error, func(Delta) error) (*Result, error) {
		panic("parsing must not infer")
	}, func(o *Options) {
		o.Catalog = catalog.Options{Sources: []catalog.Source{{Name: "models", AccountID: "account", Path: path}}, Publish: published}
	})
	body := []byte(`{"model":"model-050","input":"x","reasoning":{"effort":"high"}}`)
	var parseErr error
	allocations := testing.AllocsPerRun(3, func() { _, parseErr = h.parseRequest(body, true) })
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	// A snapshot must not be cloned once for every published model. This broad
	// ceiling separates linear copying from the old quadratic allocation path.
	if allocations > 10000 {
		t.Fatalf("quadratic catalog copying: %.0f allocations", allocations)
	}
}
