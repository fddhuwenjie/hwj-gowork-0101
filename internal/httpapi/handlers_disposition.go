package httpapi

import (
	"net/http"

	"metalmics/internal/domain"
)

var dispositionSorts = map[string]string{"id": "id", "created_at": "created_at", "status": "status"}

// ProposeDisposition POST /api/v1/lots/{id}/dispositions
func (h *Handlers) ProposeDisposition(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req dispositionReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	d := &domain.Disposition{
		LotID: id, Type: domain.DispositionType(req.Type), Reason: req.Reason, ProposedBy: actor(r),
	}
	created, err := h.exception.ProposeDisposition(r.Context(), d, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, createdResp{Data: d, Replayed: !created})
}

// ListDispositions GET /api/v1/dispositions
func (h *Handlers) ListDispositions(w http.ResponseWriter, r *http.Request) {
	p, err := pageRequest(r, "id", dispositionSorts)
	if err != nil {
		writeError(w, err)
		return
	}
	q := r.URL.Query()
	f := domain.DispositionFilter{
		LotID:  int64Query(q.Get("lot_id")),
		Type:   domain.DispositionType(q.Get("type")),
		Status: domain.DispositionStatus(q.Get("status")),
	}
	page, err := h.exception.ListDispositions(r.Context(), f, p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// ApproveDisposition POST /api/v1/dispositions/{id}/approve
func (h *Handlers) ApproveDisposition(w http.ResponseWriter, r *http.Request) {
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
	d, err := h.exception.ApproveDisposition(r.Context(), id, req.Version, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: d})
}

// RejectDisposition POST /api/v1/dispositions/{id}/reject
func (h *Handlers) RejectDisposition(w http.ResponseWriter, r *http.Request) {
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
	d, err := h.exception.RejectDisposition(r.Context(), id, req.Version, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: d})
}

// ExecuteDisposition POST /api/v1/dispositions/{id}/execute
func (h *Handlers) ExecuteDisposition(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req executeDispositionReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	d, err := h.exception.ExecuteDisposition(r.Context(), id, req.Version, actor(r), req.Resolution)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: d})
}
