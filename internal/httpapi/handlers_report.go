package httpapi

import (
	"net/http"

	"metalmics/internal/domain"
)

// ListRetestAccepted GET /api/v1/reports/retest-accepted
// 派生查询：初检不符合但复验仍接收的批次与材质证明编号。
func (h *Handlers) ListRetestAccepted(w http.ResponseWriter, r *http.Request) {
	p, err := pageRequest(r, "id", map[string]string{"id": "id"})
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := h.report.ListRetestAccepted(r.Context(), p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// CountCertMissingAccepted GET /api/v1/reports/cert-missing-accepted?days=30
// 派生查询：各供方近期材质证明缺失而先接收的批次数量。
func (h *Handlers) CountCertMissingAccepted(w http.ResponseWriter, r *http.Request) {
	days := intQuery(r.URL.Query().Get("days"), 30)
	items, err := h.report.CountCertMissingAccepted(r.Context(), days)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: items})
}

var auditSorts = map[string]string{"id": "id", "created_at": "created_at", "entity": "entity"}

// ListAuditEvents GET /api/v1/audit-events
func (h *Handlers) ListAuditEvents(w http.ResponseWriter, r *http.Request) {
	p, err := pageRequest(r, "id", auditSorts)
	if err != nil {
		writeError(w, err)
		return
	}
	q := r.URL.Query()
	since, err := timeQuery(q.Get("since"))
	if err != nil {
		writeError(w, err)
		return
	}
	f := domain.AuditFilter{
		Entity:   q.Get("entity"),
		EntityID: int64Query(q.Get("entity_id")),
		Actor:    q.Get("actor"),
		Since:    since,
	}
	page, err := h.report.ListAuditEvents(r.Context(), f, p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
