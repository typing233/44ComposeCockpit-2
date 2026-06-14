package auth

import (
	"net/http"

	"github.com/composecockpit/server/internal/domain"
)

type Permission string

const (
	PermProjectRead    Permission = "project:read"
	PermProjectOperate Permission = "project:operate"
	PermProjectAdmin   Permission = "project:admin"
	PermUserManage     Permission = "user:manage"
	PermAuditRead      Permission = "audit:read"
	PermGlobalEvents   Permission = "events:global"
)

var rolePermissions = map[domain.Role][]Permission{
	domain.RoleAdmin: {
		PermProjectRead, PermProjectOperate, PermProjectAdmin,
		PermUserManage, PermAuditRead, PermGlobalEvents,
	},
	domain.RoleOperator: {
		PermProjectRead, PermProjectOperate, PermAuditRead,
	},
	domain.RoleViewer: {
		PermProjectRead,
	},
}

func HasPermission(role domain.Role, perm Permission) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	for _, p := range perms {
		if p == perm {
			return true
		}
	}
	return false
}

func RequirePermission(perm Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				writeUnauthorized(w, "authentication required")
				return
			}

			if !HasPermission(claims.Role, perm) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":{"code":"ERR_FORBIDDEN","message":"insufficient permissions"}}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func RequireRole(roles ...domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				writeUnauthorized(w, "authentication required")
				return
			}

			for _, role := range roles {
				if claims.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":{"code":"ERR_FORBIDDEN","message":"insufficient role"}}`))
		})
	}
}
