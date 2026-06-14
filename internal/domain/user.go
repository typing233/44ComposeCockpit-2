package domain

import "time"

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ProjectACL struct {
	UserID    string    `json:"user_id"`
	ProjectID string    `json:"project_id"`
	Role      Role      `json:"role"`
	GrantedBy string    `json:"granted_by"`
	GrantedAt time.Time `json:"granted_at"`
}

type AuditEntry struct {
	ID         int64         `json:"id"`
	UserID     string        `json:"user_id"`
	Username   string        `json:"username,omitempty"`
	ProjectID  string        `json:"project_id,omitempty"`
	Operation  string        `json:"operation"`
	Scope      *OpScope      `json:"scope,omitempty"`
	Status     string        `json:"status"`
	ErrorCode  string        `json:"error_code,omitempty"`
	ErrorMsg   string        `json:"error_msg,omitempty"`
	DurationMs int           `json:"duration_ms,omitempty"`
	Metadata   interface{}   `json:"metadata,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
}

type OpScope struct {
	Services []string `json:"services,omitempty"`
}

type PaginationOpts struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}
