package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/composecockpit/server/internal/docker"
	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/pkg/apierr"
)

type ProjectResolver interface {
	GetProject(ctx context.Context, id domain.ProjectID) (*domain.Project, error)
}

type AuditLogger interface {
	LogOperation(ctx context.Context, entry domain.AuditEntry) error
}

type Executor struct {
	ops        *docker.Operations
	locker     ProjectLocker
	resolver   ProjectResolver
	auditor    AuditLogger
	logger     *slog.Logger

	mu         sync.RWMutex
	running    map[string]*runningOp
	cancelFns  map[string]context.CancelFunc
}

type runningOp struct {
	Request domain.OperationRequest
	Result  *domain.OperationResult
}

func NewExecutor(ops *docker.Operations, locker ProjectLocker, resolver ProjectResolver, auditor AuditLogger, logger *slog.Logger) *Executor {
	return &Executor{
		ops:       ops,
		locker:    locker,
		resolver:  resolver,
		auditor:   auditor,
		logger:    logger,
		running:   make(map[string]*runningOp),
		cancelFns: make(map[string]context.CancelFunc),
	}
}

func (e *Executor) Execute(ctx context.Context, req domain.OperationRequest) (*domain.OperationResult, error) {
	if req.ID == "" {
		req.ID = uuid.New().String()
	}
	if req.Timeout == 0 {
		req.Timeout = 5 * time.Minute
	}
	req.CreatedAt = time.Now()

	if locked, holder := e.locker.IsLocked(req.Scope.ProjectID); locked {
		return nil, &domain.AppError{
			Code:       apierr.ErrOperationInProgress,
			Message:    fmt.Sprintf("project is locked by operation %s", holder),
			HTTPStatus: 409,
			Details:    map[string]string{"operation_id": holder},
		}
	}

	opCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	release, err := e.locker.Acquire(opCtx, req.Scope.ProjectID, req.ID)
	if err != nil {
		return nil, err
	}
	defer release()

	e.mu.Lock()
	e.running[req.ID] = &runningOp{Request: req}
	e.cancelFns[req.ID] = cancel
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.running, req.ID)
		delete(e.cancelFns, req.ID)
		e.mu.Unlock()
	}()

	project, err := e.resolver.GetProject(ctx, req.Scope.ProjectID)
	if err != nil {
		return nil, err
	}

	startTime := time.Now()
	result, err := e.executeOp(opCtx, req, project)
	duration := time.Since(startTime)

	auditEntry := domain.AuditEntry{
		UserID:     req.UserID,
		ProjectID:  string(req.Scope.ProjectID),
		Operation:  string(req.Type),
		DurationMs: int(duration.Milliseconds()),
		CreatedAt:  time.Now(),
	}
	if req.Scope.Services != nil {
		auditEntry.Scope = &domain.OpScope{Services: req.Scope.Services}
	}

	if err != nil {
		auditEntry.Status = "failed"
		if appErr, ok := err.(*domain.AppError); ok {
			auditEntry.ErrorCode = appErr.Code
			auditEntry.ErrorMsg = appErr.Message
		} else {
			auditEntry.ErrorCode = apierr.ErrInternal
			auditEntry.ErrorMsg = err.Error()
		}
	} else if result != nil {
		auditEntry.Status = string(result.Status)
		if result.Error != nil {
			auditEntry.ErrorCode = result.Error.Code
			auditEntry.ErrorMsg = result.Error.Message
		}
	}

	if e.auditor != nil {
		_ = e.auditor.LogOperation(context.Background(), auditEntry)
	}

	if err != nil {
		return nil, err
	}

	result.RequestID = req.ID
	return result, nil
}

func (e *Executor) Cancel(opID string) error {
	e.mu.RLock()
	cancel, ok := e.cancelFns[opID]
	e.mu.RUnlock()

	if !ok {
		return &domain.AppError{
			Code:       apierr.ErrOperationNotFound,
			Message:    "operation not found or already completed",
			HTTPStatus: 404,
		}
	}

	cancel()
	e.logger.Info("operation cancelled", "operation_id", opID)
	return nil
}

func (e *Executor) GetRunningOps() []domain.OperationRequest {
	e.mu.RLock()
	defer e.mu.RUnlock()

	ops := make([]domain.OperationRequest, 0, len(e.running))
	for _, op := range e.running {
		ops = append(ops, op.Request)
	}
	return ops
}

func (e *Executor) executeOp(ctx context.Context, req domain.OperationRequest, project *domain.Project) (*domain.OperationResult, error) {
	switch req.Type {
	case domain.OpUp:
		return e.ops.Up(ctx, project, req.Scope.Services)
	case domain.OpDown:
		return e.ops.Down(ctx, project, req.Scope.Services)
	case domain.OpStart:
		return e.ops.Start(ctx, project, req.Scope.Services)
	case domain.OpStop:
		return e.ops.Stop(ctx, project, req.Scope.Services)
	case domain.OpRestart:
		return e.ops.Restart(ctx, project, req.Scope.Services)
	default:
		return nil, &domain.AppError{
			Code:       apierr.ErrValidation,
			Message:    fmt.Sprintf("unsupported operation type: %s", req.Type),
			HTTPStatus: 400,
		}
	}
}
