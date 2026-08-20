package httpapi

import (
	"net/http"

	"metalmics/internal/domain"
)

var jobSorts = map[string]string{"id": "id", "run_at": "run_at", "created_at": "created_at"}

// EnqueueJob POST /api/v1/jobs
func (h *Handlers) EnqueueJob(w http.ResponseWriter, r *http.Request) {
	var req jobReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	payload := req.Payload
	if payload == nil {
		payload = map[string]interface{}{}
	}
	job, err := h.jobs.Enqueue(r.Context(), req.Type, payload, req.MaxAttempts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createdResp{Data: job})
}

// ListJobs GET /api/v1/jobs
func (h *Handlers) ListJobs(w http.ResponseWriter, r *http.Request) {
	p, err := pageRequest(r, "id", jobSorts)
	if err != nil {
		writeError(w, err)
		return
	}
	q := r.URL.Query()
	f := domain.JobFilter{Status: domain.JobStatus(q.Get("status")), Type: q.Get("type")}
	page, err := h.jobs.ListJobs(r.Context(), f, p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// RetryJob POST /api/v1/jobs/{id}/retry
func (h *Handlers) RetryJob(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	job, err := h.jobs.RetryJob(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: job})
}
