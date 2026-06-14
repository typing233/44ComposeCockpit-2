package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/composecockpit/server/internal/domain"
)

type contextKey string

const claimsKey contextKey = "claims"

func Middleware(jwtMgr *JWTManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeUnauthorized(w, "missing authorization header")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				writeUnauthorized(w, "invalid authorization format")
				return
			}

			claims, err := jwtMgr.ValidateAccessToken(parts[1])
			if err != nil {
				writeUnauthorized(w, "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func GetClaims(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsKey).(*Claims)
	return claims
}

func GetUserID(ctx context.Context) string {
	if c := GetClaims(ctx); c != nil {
		return c.UserID
	}
	return ""
}

func GetRole(ctx context.Context) domain.Role {
	if c := GetClaims(ctx); c != nil {
		return c.Role
	}
	return ""
}

func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":{"code":"ERR_UNAUTHORIZED","message":"` + msg + `"}}`))
}
