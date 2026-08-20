package httpapi

import (
	"net/http"

	"metalmics/internal/domain"
)

var supplierSorts = map[string]string{"id": "id", "code": "code", "created_at": "created_at"}

// CreateSupplier POST /api/v1/suppliers
func (h *Handlers) CreateSupplier(w http.ResponseWriter, r *http.Request) {
	var req supplierReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	sup := &domain.Supplier{Code: req.Code, Name: req.Name, Contact: req.Contact}
	created, err := h.daily.RegisterSupplier(r.Context(), sup)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK // 幂等重放
	}
	writeJSON(w, status, createdResp{Data: sup, Replayed: !created})
}

// ListSuppliers GET /api/v1/suppliers
func (h *Handlers) ListSuppliers(w http.ResponseWriter, r *http.Request) {
	p, err := pageRequest(r, "id", supplierSorts)
	if err != nil {
		writeError(w, err)
		return
	}
	q := r.URL.Query()
	f := domain.SupplierFilter{CodePrefix: q.Get("code_prefix"), NameLike: q.Get("name_like")}
	page, err := h.daily.ListSuppliers(r.Context(), f, p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// GetSupplier GET /api/v1/suppliers/{id}
func (h *Handlers) GetSupplier(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sup, err := h.daily.GetSupplier(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, okResp{Data: sup})
}
