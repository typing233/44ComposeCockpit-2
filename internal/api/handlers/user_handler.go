package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/composecockpit/server/internal/httputil"
	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/store"
	"github.com/composecockpit/server/pkg/apierr"
)

type UserHandler struct {
	userRepo store.UserRepository
}

func NewUserHandler(userRepo store.UserRepository) *UserHandler {
	return &UserHandler{userRepo: userRepo}
}

func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	opts := parsePagination(r)
	users, total, err := h.userRepo.List(r.Context(), opts)
	if err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to list users", nil)
		return
	}
	httputil.WritePaginated(w, r, users, total, opts.Offset, opts.Limit)
}

func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string      `json:"username"`
		Email    string      `json:"email"`
		Password string      `json:"password"`
		Role     domain.Role `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "invalid request body", nil)
		return
	}

	if req.Username == "" || req.Email == "" || req.Password == "" {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "username, email, and password are required", nil)
		return
	}

	if req.Role == "" {
		req.Role = domain.RoleViewer
	}

	user := &domain.User{
		ID:        uuid.New().String(),
		Username:  req.Username,
		Email:     req.Email,
		Role:      req.Role,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := h.userRepo.Create(r.Context(), user, req.Password); err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to create user", nil)
		return
	}

	httputil.WriteJSON(w, r, http.StatusCreated, user)
}

func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")

	user, err := h.userRepo.GetByID(r.Context(), userID)
	if err != nil || user == nil {
		httputil.WriteError(w, r, http.StatusNotFound, apierr.ErrUserNotFound, "user not found", nil)
		return
	}

	var req struct {
		Username string      `json:"username,omitempty"`
		Email    string      `json:"email,omitempty"`
		Role     domain.Role `json:"role,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, r, http.StatusBadRequest, apierr.ErrValidation, "invalid request body", nil)
		return
	}

	if req.Username != "" {
		user.Username = req.Username
	}
	if req.Email != "" {
		user.Email = req.Email
	}
	if req.Role != "" {
		user.Role = req.Role
	}

	if err := h.userRepo.Update(r.Context(), user); err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to update user", nil)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, user)
}

func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")

	if err := h.userRepo.Delete(r.Context(), userID); err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "failed to delete user", nil)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func parsePagination(r *http.Request) domain.PaginationOpts {
	opts := domain.PaginationOpts{Limit: 50, Offset: 0}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n := parseInt(v); n > 0 {
			opts.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n := parseInt(v); n >= 0 {
			opts.Offset = n
		}
	}
	return opts
}

func parseInt(s string) int {
	var n int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	return n
}
