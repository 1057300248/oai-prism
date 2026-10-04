package gateway

import (
	"net/url"
	"strconv"
)

// Input-item pagination follows the public API's default descending order and
// 1..100 limit. Unknown query parameters fail instead of being silently ignored.
func inputItemsPage(snapshot Snapshot, query url.Values) (map[string]any, error) {
	for key, values := range query {
		if key != "after" && key != "limit" && key != "order" {
			return nil, unsupported(key)
		}
		if len(values) != 1 {
			return nil, bad(key, "Duplicate query parameter.")
		}
	}
	limit := 20
	if value := query.Get("limit"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			return nil, bad("limit", "Limit must be between 1 and 100.")
		}
	}
	order := query.Get("order")
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		return nil, bad("order", "Order must be asc or desc.")
	}
	// InputItems is distinct from Items, which includes the assistant output for
	// continuation. Old snapshots missing this field fail explicitly.
	if snapshot.InputItems == nil {
		return nil, &APIError{Status: 409, Code: "snapshot_version_mismatch", Message: "This snapshot predates input-item tracking."}
	}
	items := append([]Item(nil), snapshot.InputItems...)
	if order == "desc" {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	if after := query.Get("after"); after != "" {
		index := -1
		for i, item := range items {
			if item.ID == after {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, bad("after", "Cursor does not belong to this response's input items.")
		}
		items = items[index+1:]
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if items == nil {
		items = []Item{}
	}
	var first, last any
	if len(items) > 0 {
		first = items[0].ID
		last = items[len(items)-1].ID
	}
	return map[string]any{"object": "list", "data": items, "first_id": first, "last_id": last, "has_more": hasMore}, nil
}
