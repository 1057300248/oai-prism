package gateway

import (
	"context"
	"net/url"

	"github.com/oai-prism/oaiprism/internal/attachment"
)

// Response output is a separate artifact, but retrieving copied input bytes must
// not bypass the source file's expiry/deletion by going through a snapshot.
func (h *Handler) ownedInputItemsPage(ctx context.Context, snapshot Snapshot, query url.Values, owner string) (map[string]any, error) {
	for _, id := range snapshot.FileIDs {
		if h.files == nil {
			return nil, fileError(attachment.ErrNotFound)
		}
		if _, err := h.files.Get(ctx, id, owner); err != nil {
			return nil, fileError(err)
		}
	}
	return inputItemsPage(snapshot, query)
}
