package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/composecockpit/server/internal/httputil"
	"github.com/composecockpit/server/internal/auth"
	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/orchestrator"
	"github.com/composecockpit/server/pkg/apierr"
)

type OperationHandler struct {
	executor *orchestrator.Executor
}

func NewOperationHandler(executor *orchestrator.Executor) *OperationHandler {
	return &OperationHandler{executor: executor}
}

type OperationBody struct {
	Services []string `json:"services,omitempty"`
	Timeout  string   `json:"timeout,omitempty"`
}

func (h *OperationHandler) Up(w http.ResponseWriter, r *http.Request) {
	h.executeOp(w, r, domain.OpUp)
}

func (h *OperationHandler) Down(w http.ResponseWriter, r *http.Request) {
	h.executeOp(w, r, domain.OpDown)
}

func (h *OperationHandler) Start(w http.ResponseWriter, r *http.Request) {
	h.executeOp(w, r, domain.OpStart)
}

func (h *OperationHandler) Stop(w http.ResponseWriter, r *http.Request) {
	h.executeOp(w, r, domain.OpStop)
}

func (h *OperationHandler) Restart(w http.ResponseWriter, r *http.Request) {
	h.executeOp(w, r, domain.OpRestart)
}

func (h *OperationHandler) ServiceStart(w http.ResponseWriter, r *http.Request) {
	h.executeServiceOp(w, r, domain.OpStart)
}

func (h *OperationHandler) ServiceStop(w http.ResponseWriter, r *http.Request) {
	h.executeServiceOp(w, r, domain.OpStop)
}

func (h *OperationHandler) ServiceRestart(w http.ResponseWriter, r *http.Request) {
	h.executeServiceOp(w, r, domain.OpRestart)
}

func (h *OperationHandler) CancelOperation(w http.ResponseWriter, r *http.Request) {
	opID := chi.URLParam(r, "opID")

	if err := h.executor.Cancel(opID); err != nil {
		if appErr, ok := err.(*domain.AppError); ok {
			httputil.WriteError(w, r, appErr.HTTPStatus, appErr.Code, appErr.Message, nil)
			return
		}
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, err.Error(), nil)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (h *OperationHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	ops := h.executor.GetRunningOps()
	opID := chi.URLParam(r, "opID")

	for _, op := range ops {
		if op.ID == opID {
			httputil.WriteJSON(w, r, http.StatusOK, op)
			return
		}
	}

	httputil.WriteError(w, r, http.StatusNotFound, apierr.ErrOperationNotFound, "operation not found or completed", nil)
}

func (h *OperationHandler) executeOp(w http.ResponseWriter, r *http.Request, opType domain.OperationType) {
	projectID := domain.ProjectID(chi.URLParam(r, "projectID"))
	claims := auth.GetClaims(r.Context())

	var body OperationBody
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}

	timeout := 5 * time.Minute
	if body.Timeout != "" {
		if d, err := time.ParseDuration(body.Timeout); err == nil {
			timeout = d
		}
	}

	priority := 50
	if claims.Role == domain.RoleAdmin {
		priority = 100
	}

	req := domain.OperationRequest{
		ID:       uuid.New().String(),
		Type:     opType,
		Scope:    domain.OperationScope{ProjectID: projectID, Services: body.Services},
		UserID:   claims.UserID,
		Priority: priority,
		Timeout:  timeout,
	}

	result, err := h.executor.Execute(r.Context(), req)
	if err != nil {
		if appErr, ok := err.(*domain.AppError); ok {
			httputil.WriteError(w, r, appErr.HTTPStatus, appErr.Code, appErr.Message, appErr.Details)
			return
		}
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, err.Error(), nil)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, result)
}

func (h *OperationHandler) executeServiceOp(w http.ResponseWriter, r *http.Request, opType domain.OperationType) {
	projectID := domain.ProjectID(chi.URLParam(r, "projectID"))
	serviceName := chi.URLParam(r, "serviceName")
	claims := auth.GetClaims(r.Context())

	priority := 50
	if claims.Role == domain.RoleAdmin {
		priority = 100
	}

	req := domain.OperationRequest{
		ID:       uuid.New().String(),
		Type:     opType,
		Scope:    domain.OperationScope{ProjectID: projectID, Services: []string{serviceName}},
		UserID:   claims.UserID,
		Priority: priority,
		Timeout:  5 * time.Minute,
	}

	result, err := h.executor.Execute(r.Context(), req)
	if err != nil {
		if appErr, ok := err.(*domain.AppError); ok {
			httputil.WriteError(w, r, appErr.HTTPStatus, appErr.Code, appErr.Message, appErr.Details)
			return
		}
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, err.Error(), nil)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, result)
}
