package httpapi

import (
	"net/http"

	"metalmics/internal/domain"
)

// SubmitSpectrumReport POST /api/v1/samples/{id}/spectrum-reports
func (h *Handlers) SubmitSpectrumReport(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req spectrumReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	rep := &domain.SpectrumReport{
		ReportNo: req.ReportNo, SampleID: id, Readings: req.Readings, Analyzer: req.Analyzer,
	}
	created, err := h.daily.SubmitSpectrumReport(r.Context(), rep, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, createdResp{Data: rep, Replayed: !created})
}

// ListSpectrumBySample GET /api/v1/samples/{id}/spectrum-reports
func (h *Handlers) ListSpectrumBySample(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := h.daily.ListSpectrumBySample(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: items})
}

// RegisterCertificate POST /api/v1/lots/{id}/certificates
func (h *Handlers) RegisterCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var req certificateReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	cert := &domain.MillCertificate{
		CertNo: req.CertNo, LotID: id, Grade: req.Grade, HeatNo: req.HeatNo,
		Elements: req.Elements, IssuedAt: req.IssuedAt,
	}
	created, err := h.daily.RegisterCertificate(r.Context(), cert, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, createdResp{Data: cert, Replayed: !created})
}

// ListCertificates GET /api/v1/lots/{id}/certificates
func (h *Handlers) ListCertificates(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := h.daily.ListCertificates(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: items})
}

// VerifyCertificate POST /api/v1/certificates/{id}/verify
func (h *Handlers) VerifyCertificate(w http.ResponseWriter, r *http.Request) {
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
	cert, err := h.daily.VerifyCertificate(r.Context(), id, req.Version, actor(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: cert})
}
