package gateway

import (
	"encoding/json"
	"testing"
)

func TestRevokedFileBlocksSavedInputItemsButNotIndependentAnswer(t *testing.T) {
	h := fileHarness(t, okEngine)
	u := upload(t, h, "private.txt", []byte("file-secret-input-812"), "tenant", nil)
	if u.Code != 200 {
		t.Fatal(u.Body.String())
	}
	id := readMap(t, u)["id"].(string)
	body, _ := json.Marshal(map[string]any{"model": "test-model", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_file", "file_id": id}}}}, "store": true})
	response := call(h, "POST", "/v1/responses", string(body), "key-a", "tenant")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	rid := readMap(t, response)["id"].(string)
	if call(h, "GET", "/v1/responses/"+rid+"/input_items", "", "key-a", "tenant").Code != 200 {
		t.Fatal("live file inputs inaccessible")
	}
	if call(h, "DELETE", "/v1/files/"+id, "", "key-a", "tenant").Code != 200 {
		t.Fatal("delete failed")
	}
	oldInputs := call(h, "GET", "/v1/responses/"+rid+"/input_items", "", "key-a", "tenant")
	if oldInputs.Code != 404 {
		t.Fatalf("saved input copied revoked bytes: %d %s", oldInputs.Code, oldInputs.Body)
	}
	// The response answer is separately retained under its own expiry and deletion.
	if call(h, "GET", "/v1/responses/"+rid, "", "key-a", "tenant").Code != 200 {
		t.Fatal("independent answer wrongly deleted")
	}
}
