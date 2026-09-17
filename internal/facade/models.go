package facade

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

// ModelInfo 是 /v1/models 的条目。
//
// Name 是展示名（label），与 ID 分离：ID 是调用时要传的名字，
// Name 给 UI 列表用。没配 label 的模型不带 name 字段。
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	Name    string `json:"name,omitempty"`
}

// ModelList 是 /v1/models 的返回。
type ModelList struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// handleModels 列出对外可用的模型名。
//
// 这里列的是"映射表里的对外名"，而不是上游真实模型名。
// 客户端按这个列表选模型，我们的门面负责翻译。
func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	f := &h.cfg.Facade

	names := make([]string, 0, len(f.Models)+1)
	for name := range f.Models {
		names = append(names, name)
	}
	// 保证默认模型一定在列表里（可能没显式写进映射表）。
	found := false
	for _, n := range names {
		if n == f.DefaultModel {
			found = true
			break
		}
	}
	if !found && f.DefaultModel != "" {
		names = append(names, f.DefaultModel)
	}
	sort.Strings(names)

	created := time.Now().Unix()
	data := make([]ModelInfo, 0, len(names))
	for _, n := range names {
		info := ModelInfo{
			ID:      n,
			Object:  "model",
			Created: created,
			OwnedBy: "oaiprism",
		}
		if m, ok := f.Models[n]; ok {
			info.Name = m.Label
		}
		data = append(data, info)
	}
	writeJSON(w, http.StatusOK, ModelList{Object: "list", Data: data})
}

// handleModelByID 返回单个模型信息。
func (h *Handler) handleModelByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "缺少模型 ID")
		return
	}
	f := &h.cfg.Facade
	if _, ok := f.Models[id]; !ok && id != f.DefaultModel {
		writeError(w, http.StatusNotFound, "invalid_request_error",
			"未知模型 "+id+"（可用模型见 GET /v1/models）")
		return
	}
	info := ModelInfo{
		ID:      id,
		Object:  "model",
		Created: time.Now().Unix(),
		OwnedBy: "oaiprism",
	}
	if m, ok := f.Models[id]; ok {
		info.Name = m.Label
	}
	writeJSON(w, http.StatusOK, info)
}
