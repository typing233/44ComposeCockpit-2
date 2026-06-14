package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/composecockpit/server/internal/httputil"
	"github.com/composecockpit/server/internal/auth"
	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/store"
	"github.com/composecockpit/server/pkg/apierr"
)

type AuthHandler struct {
	userRepo   store.UserRepository
	jwtManager *auth.JWTManager
}

func NewAuthHandler(userRepo store.UserRepository, jwtManager *auth.JWTManager) *AuthHandler {
	return &AuthHandler{userRepo: userRepo, jwtManager: jwtManager}
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "invalid request body", nil)
		return
	}

	if req.Username == "" || req.Password == "" {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "username and password are required", nil)
		return
	}

	user, err := h.userRepo.ValidatePassword(r.Context(), req.Username, req.Password)
	if err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "internal error", nil)
		return
	}
	if user == nil {
		httputil.WriteError(w, r, http.StatusUnauthorized, apierr.ErrUnauthorized, "invalid credentials", nil)
		return
	}

	tokens, err := h.jwtManager.GenerateTokenPair(user)
	if err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to generate tokens", nil)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, tokens)
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "invalid request body", nil)
		return
	}

	userID, err := h.jwtManager.ValidateRefreshToken(req.RefreshToken)
	if err != nil {
		httputil.WriteError(w, r, http.StatusUnauthorized, apierr.ErrTokenInvalid, "invalid refresh token", nil)
		return
	}

	user, err := h.userRepo.GetByID(r.Context(), userID)
	if err != nil || user == nil {
		httputil.WriteError(w, r, http.StatusUnauthorized, apierr.ErrUnauthorized, "user not found", nil)
		return
	}

	tokens, err := h.jwtManager.GenerateTokenPair(user)
	if err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to generate tokens", nil)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, tokens)
}

type RegisterRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "invalid request body", nil)
		return
	}

	if req.Username == "" || req.Email == "" || req.Password == "" {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "username, email and password are required", nil)
		return
	}

	if len(req.Password) < 8 {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "password must be at least 8 characters", nil)
		return
	}

	existing, _ := h.userRepo.GetByUsername(r.Context(), req.Username)
	if existing != nil {
		httputil.WriteError(w, r, http.StatusConflict, "ERR_USER_EXISTS", "username already taken", nil)
		return
	}

	user := &domain.User{
		ID:        uuid.New().String(),
		Username:  req.Username,
		Email:     req.Email,
		Role:      domain.RoleViewer,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := h.userRepo.Create(r.Context(), user, req.Password); err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to create user", nil)
		return
	}

	httputil.WriteJSON(w, r, http.StatusCreated, map[string]interface{}{
		"id":       user.ID,
		"username": user.Username,
		"email":    user.Email,
		"role":     user.Role,
	})
}
