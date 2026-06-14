package domain

import "time"

type OperationType string

const (
	OpUp      OperationType = "up"
	OpDown    OperationType = "down"
	OpStart   OperationType = "start"
	OpStop    OperationType = "stop"
	OpRestart OperationType = "restart"
	OpPull    OperationType = "pull"
)

type OpStatus string

const (
	OpStatusPending    OpStatus = "pending"
	OpStatusRunning    OpStatus = "running"
	OpStatusSucceeded  OpStatus = "succeeded"
	OpStatusFailed     OpStatus = "failed"
	OpStatusCancelled  OpStatus = "cancelled"
	OpStatusRolledBack OpStatus = "rolled_back"
)

type OperationScope struct {
	ProjectID ProjectID `json:"project_id"`
	Services  []string  `json:"services,omitempty"`
}

type OperationRequest struct {
	ID        string         `json:"id"`
	Type      OperationType  `json:"type"`
	Scope     OperationScope `json:"scope"`
	UserID    string         `json:"user_id"`
	Priority  int            `json:"priority"`
	Timeout   time.Duration  `json:"timeout"`
	CreatedAt time.Time      `json:"created_at"`
}

type OperationResult struct {
	RequestID  string     `json:"request_id"`
	Status     OpStatus   `json:"status"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      *OpError   `json:"error,omitempty"`
	Steps      []OpStep   `json:"steps"`
}

type OpError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

type OpStep struct {
	Service  string        `json:"service"`
	Action   string        `json:"action"`
	Status   OpStatus      `json:"status"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration_ms"`
}
