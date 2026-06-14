package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/composecockpit/server/internal/auth"
	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/httputil"
	"github.com/composecockpit/server/internal/store"
	"github.com/composecockpit/server/pkg/apierr"
)

type ACLHandler struct {
	aclRepo store.ACLRepository
}

func NewACLHandler(aclRepo store.ACLRepository) *ACLHandler {
	return &ACLHandler{aclRepo: aclRepo}
}

type GrantACLRequest struct {
	UserID string      `json:"user_id"`
	Role   domain.Role `json:"role"`
}

func (h *ACLHandler) Grant(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	claims := auth.GetClaims(r.Context())

	var req GrantACLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "invalid request body", nil)
		return
	}

	if req.UserID == "" {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "user_id is required", nil)
		return
	}
	if req.Role != domain.RoleOperator && req.Role != domain.RoleViewer {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "role must be 'operator' or 'viewer'", nil)
		return
	}

	acl := domain.ProjectACL{
		UserID:    req.UserID,
		ProjectID: projectID,
		Role:      req.Role,
		GrantedBy: claims.UserID,
		GrantedAt: time.Now(),
	}

	if err := h.aclRepo.SetProjectACL(r.Context(), acl); err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to set ACL", nil)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, acl)
}

func (h *ACLHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	userID := chi.URLParam(r, "userID")

	if err := h.aclRepo.Delete(r.Context(), userID, projectID); err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to revoke ACL", nil)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "revoked"})
}

func (h *ACLHandler) ListByProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")

	acls, err := h.aclRepo.ListByProject(r.Context(), projectID)
	if err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to list ACLs", nil)
		return
	}
	if acls == nil {
		acls = []domain.ProjectACL{}
	}

	httputil.WriteJSON(w, r, http.StatusOK, acls)
}
