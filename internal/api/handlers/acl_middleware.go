package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/composecockpit/server/internal/auth"
	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/httputil"
	"github.com/composecockpit/server/internal/store"
	"github.com/composecockpit/server/pkg/apierr"
)

// ProjectACLMiddleware enforces project-level permissions. Admin can access
// all projects; other roles need an explicit ACL entry.
func ProjectACLMiddleware(aclRepo store.ACLRepository, requiredPerm auth.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := auth.GetClaims(r.Context())
			if claims == nil {
				httputil.WriteError(w, r, http.StatusUnauthorized, apierr.ErrUnauthorized, "authentication required", nil)
				return
			}

			// Admins bypass project ACL
			if claims.Role == domain.RoleAdmin {
				next.ServeHTTP(w, r)
				return
			}

			projectID := chi.URLParam(r, "projectID")
			if projectID == "" {
				next.ServeHTTP(w, r)
				return
			}

			role, _ := aclRepo.GetUserProjectRole(r.Context(), claims.UserID, projectID)
			if role == "" {
				httputil.WriteError(w, r, http.StatusForbidden, apierr.ErrForbidden, "no access to this project", nil)
				return
			}

			// Check if project role satisfies the required permission
			if requiredPerm == auth.PermProjectOperate && role == domain.RoleViewer {
				httputil.WriteError(w, r, http.StatusForbidden, apierr.ErrForbidden, "viewer cannot perform operations", nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
