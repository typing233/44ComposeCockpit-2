package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/composecockpit/server/internal/httputil"
	"github.com/composecockpit/server/internal/store"
	"github.com/composecockpit/server/pkg/apierr"
)

type AuditHandler struct {
	auditRepo store.AuditRepository
}

func NewAuditHandler(auditRepo store.AuditRepository) *AuditHandler {
	return &AuditHandler{auditRepo: auditRepo}
}

func (h *AuditHandler) ListAll(w http.ResponseWriter, r *http.Request) {
	opts := parsePagination(r)
	entries, total, err := h.auditRepo.List(r.Context(), opts)
	if err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to query audit log", nil)
		return
	}
	httputil.WritePaginated(w, r, entries, total, opts.Offset, opts.Limit)
}

func (h *AuditHandler) ListByProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	opts := parsePagination(r)

	entries, total, err := h.auditRepo.ListByProject(r.Context(), projectID, opts)
	if err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to query audit log", nil)
		return
	}
	httputil.WritePaginated(w, r, entries, total, opts.Offset, opts.Limit)
}

// Helpers re-used across handler files in this package
func parseIntParam(r *http.Request, key string, defaultVal int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}
