package gateway

import "github.com/oai-prism/oaiprism/internal/catalog"

func (h *Handler) codexModelWithMedia(m catalog.Model) map[string]any {
	out := CodexModel(m, h.options.CodexTools && h.options.PromptTools)
	if !h.options.InlineImages {
		return out
	}
	// A model may have both text-only and vision accounts. Request-time routing
	// further narrows the selected account set using the actual attached media.
	image := false
	for _, r := range h.catalog.Records() {
		if r.ID != m.ID || r.UpstreamID != m.UpstreamID || r.Stale {
			continue
		}
		for _, mode := range r.InputModalities {
			image = image || mode == "image"
		}
	}
	if image {
		out["input_modalities"] = []string{"text", "image"}
	}
	return out
}
