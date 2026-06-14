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
	ops      *docker.Operations
	locker   ProjectLocker
	resolver ProjectResolver
	auditor  AuditLogger
	queue    *PriorityQueue
	logger   *slog.Logger

	mu        sync.RWMutex
	running   map[string]*runningOp
	cancelFns map[string]context.CancelFunc

	// Completed operations history (ring buffer)
	historyMu sync.RWMutex
	history   []OperationRecord
	maxHistory int
}

type runningOp struct {
	Request domain.OperationRequest
	Result  *domain.OperationResult
}

type OperationRecord struct {
	Request  domain.OperationRequest `json:"request"`
	Result   *domain.OperationResult `json:"result"`
	Error    string                  `json:"error,omitempty"`
	Finished time.Time               `json:"finished"`
}

func NewExecutor(ops *docker.Operations, locker ProjectLocker, resolver ProjectResolver, auditor AuditLogger, logger *slog.Logger) *Executor {
	e := &Executor{
		ops:        ops,
		locker:     locker,
		resolver:   resolver,
		auditor:    auditor,
		queue:      NewPriorityQueue(),
		logger:     logger,
		running:    make(map[string]*runningOp),
		cancelFns:  make(map[string]context.CancelFunc),
		history:    make([]OperationRecord, 0, 256),
		maxHistory: 256,
	}
	return e
}

// StartWorkers starts N background workers that pull from the priority queue.
func (e *Executor) StartWorkers(ctx context.Context, numWorkers int) {
	for i := 0; i < numWorkers; i++ {
		go e.worker(ctx, i)
	}
}

func (e *Executor) worker(ctx context.Context, id int) {
	for {
		req, ok := e.queue.Dequeue()
		if !ok {
			return
		}
		if ctx.Err() != nil {
			return
		}
		e.executeQueued(ctx, req)
	}
}

// Submit enqueues an operation for async execution by workers.
// Returns the operation ID immediately. Use GetOperation to poll result.
func (e *Executor) Submit(req domain.OperationRequest) string {
	if req.ID == "" {
		req.ID = uuid.New().String()
	}
	if req.Timeout == 0 {
		req.Timeout = 5 * time.Minute
	}
	req.CreatedAt = time.Now()
	e.queue.Enqueue(req)
	return req.ID
}

// Execute runs an operation synchronously (blocking), used for direct API calls.
func (e *Executor) Execute(ctx context.Context, req domain.OperationRequest) (*domain.OperationResult, error) {
	if req.ID == "" {
		req.ID = uuid.New().String()
	}
	if req.Timeout == 0 {
		req.Timeout = 5 * time.Minute
	}
	req.CreatedAt = time.Now()

	// Check if project already locked
	if locked, holder := e.locker.IsLocked(req.Scope.ProjectID); locked {
		return nil, &domain.AppError{
			Code:       apierr.ErrOperationInProgress,
			Message:    fmt.Sprintf("project is locked by operation %s", holder),
			HTTPStatus: 409,
			Details:    map[string]string{"operation_id": holder},
		}
	}

	return e.run(ctx, req)
}

func (e *Executor) executeQueued(ctx context.Context, req domain.OperationRequest) {
	result, err := e.run(ctx, req)
	record := OperationRecord{
		Request:  req,
		Result:   result,
		Finished: time.Now(),
	}
	if err != nil {
		record.Error = err.Error()
	}
	e.addHistory(record)
}

func (e *Executor) run(ctx context.Context, req domain.OperationRequest) (*domain.OperationResult, error) {
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
	result, execErr := e.executeOp(opCtx, req, project)
	duration := time.Since(startTime)

	// Audit
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
	if execErr != nil {
		auditEntry.Status = "failed"
		if appErr, ok := execErr.(*domain.AppError); ok {
			auditEntry.ErrorCode = appErr.Code
			auditEntry.ErrorMsg = appErr.Message
		} else {
			auditEntry.ErrorCode = apierr.ErrInternal
			auditEntry.ErrorMsg = execErr.Error()
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

	if execErr != nil {
		return nil, execErr
	}

	result.RequestID = req.ID
	// Store in history
	e.addHistory(OperationRecord{
		Request:  req,
		Result:   result,
		Finished: time.Now(),
	})
	return result, nil
}

func (e *Executor) Cancel(opID string) error {
	e.mu.RLock()
	cancel, ok := e.cancelFns[opID]
	e.mu.RUnlock()

	if !ok {
		// Also check queue
		if req, removed := e.queue.RemoveAndReturn(opID); removed {
			e.logger.Info("operation removed from queue", "operation_id", opID)
			e.addHistory(OperationRecord{
				Request: req,
				Result: &domain.OperationResult{
					RequestID:  opID,
					Status:     domain.OpStatusCancelled,
					FinishedAt: timePtr(time.Now()),
				},
				Finished: time.Now(),
			})
			return nil
		}
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

func timePtr(t time.Time) *time.Time { return &t }

func (e *Executor) GetRunningOps() []domain.OperationRequest {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ops := make([]domain.OperationRequest, 0, len(e.running))
	for _, op := range e.running {
		ops = append(ops, op.Request)
	}
	return ops
}

// GetOperation returns a running or completed operation by ID.
func (e *Executor) GetOperation(opID string) (*OperationRecord, bool) {
	// Check running
	e.mu.RLock()
	if op, ok := e.running[opID]; ok {
		e.mu.RUnlock()
		return &OperationRecord{
			Request: op.Request,
			Result:  &domain.OperationResult{RequestID: opID, Status: domain.OpStatusRunning},
		}, true
	}
	e.mu.RUnlock()

	// Check history
	e.historyMu.RLock()
	defer e.historyMu.RUnlock()
	for i := len(e.history) - 1; i >= 0; i-- {
		if e.history[i].Request.ID == opID {
			return &e.history[i], true
		}
	}

	return nil, false
}

// GetQueueLen returns the current queue length.
func (e *Executor) GetQueueLen() int {
	return e.queue.Len()
}

func (e *Executor) addHistory(record OperationRecord) {
	e.historyMu.Lock()
	defer e.historyMu.Unlock()
	if len(e.history) >= e.maxHistory {
		e.history = e.history[1:]
	}
	e.history = append(e.history, record)
}

func (e *Executor) Close() {
	e.queue.Close()
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
