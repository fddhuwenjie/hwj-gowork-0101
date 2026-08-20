package httpapi

import (
	"net/http"

	"metalmics/internal/domain"
)

var retestSorts = map[string]string{"id": "id", "created_at": "created_at", "status": "status"}

// RequestRetest POST /api/v1/lots/{id}/retests
func (h *Handlers) RequestRetest(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req retestReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	task := &domain.RetestTask{
		LotID: id, SampleID: req.SampleID, Reason: req.Reason, RequestedBy: actor(r),
	}
	created, err := h.review.RequestRetest(r.Context(), task, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, createdResp{Data: task, Replayed: !created})
}

// ListRetests GET /api/v1/retests
func (h *Handlers) ListRetests(w http.ResponseWriter, r *http.Request) {
	p, err := pageRequest(r, "id", retestSorts)
	if err != nil {
		writeError(w, err)
		return
	}
	q := r.URL.Query()
	f := domain.RetestFilter{LotID: int64Query(q.Get("lot_id")), Status: domain.RetestStatus(q.Get("status"))}
	page, err := h.review.ListRetests(r.Context(), f, p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// ApproveRetest POST /api/v1/retests/{id}/approve
func (h *Handlers) ApproveRetest(w http.ResponseWriter, r *http.Request) {
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
	task, err := h.review.ApproveRetest(r.Context(), id, req.Version, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: task})
}

// ConcludeRetest POST /api/v1/retests/{id}/conclude
func (h *Handlers) ConcludeRetest(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req concludeRetestReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	conclusion, err := h.review.ConcludeRetest(r.Context(), id, req.Version,
		domain.ConclusionResult(req.Result), req.DecidedBy, req.CoDecidedBy, req.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: conclusion})
}
