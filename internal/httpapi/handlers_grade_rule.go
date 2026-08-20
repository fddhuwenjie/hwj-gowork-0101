package httpapi

import (
	"net/http"

	"metalmics/internal/domain"
)

var gradeRuleSorts = map[string]string{
	"id": "id", "grade": "grade", "version_no": "version_no", "created_at": "created_at",
}

// CreateGradeRule POST /api/v1/grade-rules
func (h *Handlers) CreateGradeRule(w http.ResponseWriter, r *http.Request) {
	var req gradeRuleReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	rule := &domain.GradeRule{
		Grade: req.Grade, VersionNo: req.VersionNo, Elements: req.Elements, Remark: req.Remark,
	}
	created, err := h.daily.CreateGradeRule(r.Context(), rule, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, createdResp{Data: rule, Replayed: !created})
}

// ListGradeRules GET /api/v1/grade-rules
func (h *Handlers) ListGradeRules(w http.ResponseWriter, r *http.Request) {
	p, err := pageRequest(r, "id", gradeRuleSorts)
	if err != nil {
		writeError(w, err)
		return
	}
	q := r.URL.Query()
	f := domain.GradeRuleFilter{Grade: q.Get("grade"), Status: domain.RuleStatus(q.Get("status"))}
	page, err := h.daily.ListGradeRules(r.Context(), f, p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// ActivateGradeRule POST /api/v1/grade-rules/{id}/activate
func (h *Handlers) ActivateGradeRule(w http.ResponseWriter, r *http.Request) {
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
	rule, err := h.daily.ActivateGradeRule(r.Context(), id, req.Version, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: rule})
}

// RetireGradeRule POST /api/v1/grade-rules/{id}/retire
func (h *Handlers) RetireGradeRule(w http.ResponseWriter, r *http.Request) {
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
	rule, err := h.daily.RetireGradeRule(r.Context(), id, req.Version, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: rule})
}
