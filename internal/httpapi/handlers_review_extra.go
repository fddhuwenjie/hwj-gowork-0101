package httpapi

import (
	"net/http"
)

// GetLotDetail GET /api/v1/lots/{id}/detail — 批次全链路聚合视图。
func (h *Handlers) GetLotDetail(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	detail, err := h.daily.GetLotDetail(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: detail})
}

// RejectLotReq 是拒收请求体。
type rejectLotReq struct {
	Version int64  `json:"version"`
	Reason  string `json:"reason"`
}

// RejectLot POST /api/v1/lots/{id}/reject
func (h *Handlers) RejectLot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req rejectLotReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	lot, err := h.daily.RejectLot(r.Context(), id, req.Version, actor(r), req.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: lot})
}

// RejectRetest POST /api/v1/retests/{id}/reject
func (h *Handlers) RejectRetest(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req versionReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	task, err := h.review.RejectRetest(r.Context(), id, req.Version, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: task})
}
