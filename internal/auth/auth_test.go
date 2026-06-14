package auth_test

import (
	"testing"
	"time"

	"github.com/composecockpit/server/internal/auth"
	"github.com/composecockpit/server/internal/domain"
)

func TestJWTManager_GenerateAndValidate(t *testing.T) {
	mgr := auth.NewJWTManager("test-secret-at-least-32-characters!!", 15*time.Minute, 24*time.Hour)

	user := &domain.User{
		ID:       "user-123",
		Username: "testuser",
		Role:     domain.RoleOperator,
	}

	tokens, err := mgr.GenerateTokenPair(user)
	if err != nil {
		t.Fatalf("generate token pair: %v", err)
	}

	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatal("tokens should not be empty")
	}

	claims, err := mgr.ValidateAccessToken(tokens.AccessToken)
	if err != nil {
		t.Fatalf("validate access token: %v", err)
	}

	if claims.UserID != "user-123" {
		t.Errorf("expected user ID user-123, got %s", claims.UserID)
	}
	if claims.Username != "testuser" {
		t.Errorf("expected username testuser, got %s", claims.Username)
	}
	if claims.Role != domain.RoleOperator {
		t.Errorf("expected role operator, got %s", claims.Role)
	}
}

func TestJWTManager_ExpiredToken(t *testing.T) {
	mgr := auth.NewJWTManager("test-secret-at-least-32-characters!!", -1*time.Hour, 24*time.Hour)

	user := &domain.User{ID: "user-1", Username: "test", Role: domain.RoleViewer}
	tokens, _ := mgr.GenerateTokenPair(user)

	_, err := mgr.ValidateAccessToken(tokens.AccessToken)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestJWTManager_InvalidSignature(t *testing.T) {
	mgr1 := auth.NewJWTManager("secret-one-at-least-32-characters!!!", 15*time.Minute, 24*time.Hour)
	mgr2 := auth.NewJWTManager("secret-two-at-least-32-characters!!!", 15*time.Minute, 24*time.Hour)

	user := &domain.User{ID: "user-1", Username: "test", Role: domain.RoleViewer}
	tokens, _ := mgr1.GenerateTokenPair(user)

	_, err := mgr2.ValidateAccessToken(tokens.AccessToken)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestRBAC_Permissions(t *testing.T) {
	tests := []struct {
		role   domain.Role
		perm   auth.Permission
		expect bool
	}{
		{domain.RoleAdmin, auth.PermProjectRead, true},
		{domain.RoleAdmin, auth.PermProjectOperate, true},
		{domain.RoleAdmin, auth.PermUserManage, true},
		{domain.RoleOperator, auth.PermProjectRead, true},
		{domain.RoleOperator, auth.PermProjectOperate, true},
		{domain.RoleOperator, auth.PermUserManage, false},
		{domain.RoleViewer, auth.PermProjectRead, true},
		{domain.RoleViewer, auth.PermProjectOperate, false},
		{domain.RoleViewer, auth.PermUserManage, false},
	}

	for _, tc := range tests {
		got := auth.HasPermission(tc.role, tc.perm)
		if got != tc.expect {
			t.Errorf("HasPermission(%s, %s) = %v, want %v", tc.role, tc.perm, got, tc.expect)
		}
	}
}
