package httpapi

import (
	"net/http"
	"time"

	"metalmics/internal/domain"
)

var lotSorts = map[string]string{
	"id": "id", "lot_no": "lot_no", "grade": "grade", "received_at": "received_at", "status": "status",
}

// RegisterLot POST /api/v1/lots
func (h *Handlers) RegisterLot(w http.ResponseWriter, r *http.Request) {
	var req lotReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	receivedAt := req.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	lot := &domain.MaterialLot{
		LotNo: req.LotNo, SupplierID: req.SupplierID, HeatNo: req.HeatNo,
		Grade: req.Grade, Quantity: req.Quantity, ReceivedAt: receivedAt,
	}
	created, err := h.daily.RegisterLot(r.Context(), lot, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, createdResp{Data: lot, Replayed: !created})
}

// ListLots GET /api/v1/lots
func (h *Handlers) ListLots(w http.ResponseWriter, r *http.Request) {
	p, err := pageRequest(r, "id", lotSorts)
	if err != nil {
		writeError(w, err)
		return
	}
	q := r.URL.Query()
	after, err := timeQuery(q.Get("received_after"))
	if err != nil {
		writeError(w, err)
		return
	}
	before, err := timeQuery(q.Get("received_before"))
	if err != nil {
		writeError(w, err)
		return
	}
	f := domain.LotFilter{
		Status:         domain.LotStatus(q.Get("status")),
		SupplierID:     int64Query(q.Get("supplier_id")),
		Grade:          q.Get("grade"),
		LotNoPrefix:    q.Get("lot_no_prefix"),
		ReceivedAfter:  after,
		ReceivedBefore: before,
	}
	page, err := h.daily.ListLots(r.Context(), f, p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// GetLot GET /api/v1/lots/{id}
func (h *Handlers) GetLot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	lot, err := h.daily.GetLot(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: lot})
}

// CreateSamplingPlan POST /api/v1/lots/{id}/sampling-plans
func (h *Handlers) CreateSamplingPlan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req samplingPlanReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	plan := &domain.SamplingPlan{
		PlanNo: req.PlanNo, LotID: id, RequiredCount: req.RequiredCount,
		RetainLocation: req.RetainLocation, CreatedBy: actor(r),
	}
	created, err := h.daily.CreateSamplingPlan(r.Context(), plan, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, createdResp{Data: plan, Replayed: !created})
}

// RegisterSamples POST /api/v1/sampling-plans/{id}/samples
func (h *Handlers) RegisterSamples(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req samplesReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	samples := make([]*domain.Sample, 0, len(req.Samples))
	for _, item := range req.Samples {
		samples = append(samples, &domain.Sample{SampleNo: item.SampleNo, Retained: item.Retained})
	}
	inserted, err := h.daily.RegisterSamples(r.Context(), id, samples, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, okResp{Data: map[string]interface{}{"inserted": inserted}})
}

// CompleteSampling POST /api/v1/lots/{id}/sampling-complete
func (h *Handlers) CompleteSampling(w http.ResponseWriter, r *http.Request) {
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
	lot, err := h.daily.CompleteSampling(r.Context(), id, req.Version, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: lot})
}

// AnalyzeLot POST /api/v1/lots/{id}/analyze
func (h *Handlers) AnalyzeLot(w http.ResponseWriter, r *http.Request) {
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
	lot, err := h.daily.AnalyzeLot(r.Context(), id, req.Version, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: lot})
}

// JudgeLot POST /api/v1/lots/{id}/judge
func (h *Handlers) JudgeLot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req judgeReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	conclusion, err := h.daily.JudgeLot(r.Context(), id, req.Version, req.DecidedBy, req.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: conclusion})
}

// AcceptLot POST /api/v1/lots/{id}/accept
func (h *Handlers) AcceptLot(w http.ResponseWriter, r *http.Request) {
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
	lot, err := h.daily.AcceptLot(r.Context(), id, req.Version, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: lot})
}

// BatchAccept POST /api/v1/lots/batch-accept
func (h *Handlers) BatchAccept(w http.ResponseWriter, r *http.Request) {
	var req batchAcceptReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	result, err := h.daily.BatchAccept(r.Context(), req.LotIDs, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: result})
}

// ListLotConclusions GET /api/v1/lots/{id}/conclusions
func (h *Handlers) ListLotConclusions(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := h.daily.ListConclusions(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: items})
}
