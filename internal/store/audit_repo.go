package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/composecockpit/server/internal/domain"
)

type AuditRepository interface {
	LogOperation(ctx context.Context, entry domain.AuditEntry) error
	ListByProject(ctx context.Context, projectID string, opts domain.PaginationOpts) ([]domain.AuditEntry, int, error)
	ListByUser(ctx context.Context, userID string, opts domain.PaginationOpts) ([]domain.AuditEntry, int, error)
	List(ctx context.Context, opts domain.PaginationOpts) ([]domain.AuditEntry, int, error)
}

type auditRepo struct {
	db *DB
}

func NewAuditRepository(db *DB) AuditRepository {
	return &auditRepo{db: db}
}

func (r *auditRepo) LogOperation(ctx context.Context, entry domain.AuditEntry) error {
	var scopeJSON []byte
	if entry.Scope != nil {
		scopeJSON, _ = json.Marshal(entry.Scope)
	}

	_, err := r.db.Pool.Exec(ctx,
		`INSERT INTO audit_log (user_id, project_id, operation, scope, status, error_code, error_msg, duration_ms, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		nilIfEmpty(entry.UserID),
		nilIfEmpty(entry.ProjectID),
		entry.Operation,
		scopeJSON,
		entry.Status,
		nilIfEmpty(entry.ErrorCode),
		nilIfEmpty(entry.ErrorMsg),
		entry.DurationMs,
		entry.CreatedAt,
	)
	return err
}

func (r *auditRepo) ListByProject(ctx context.Context, projectID string, opts domain.PaginationOpts) ([]domain.AuditEntry, int, error) {
	opts = normalizeOpts(opts)

	var total int
	err := r.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log WHERE project_id = $1`, projectID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := r.db.Pool.Query(ctx,
		`SELECT id, user_id, project_id, operation, scope, status, error_code, error_msg, duration_ms, created_at
		 FROM audit_log WHERE project_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		projectID, opts.Limit, opts.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	entries, err := scanAuditEntries(rows)
	return entries, total, err
}

func (r *auditRepo) ListByUser(ctx context.Context, userID string, opts domain.PaginationOpts) ([]domain.AuditEntry, int, error) {
	opts = normalizeOpts(opts)

	var total int
	err := r.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log WHERE user_id = $1`, userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := r.db.Pool.Query(ctx,
		`SELECT id, user_id, project_id, operation, scope, status, error_code, error_msg, duration_ms, created_at
		 FROM audit_log WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, opts.Limit, opts.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	entries, err := scanAuditEntries(rows)
	return entries, total, err
}

func (r *auditRepo) List(ctx context.Context, opts domain.PaginationOpts) ([]domain.AuditEntry, int, error) {
	opts = normalizeOpts(opts)

	var total int
	err := r.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := r.db.Pool.Query(ctx,
		`SELECT id, user_id, project_id, operation, scope, status, error_code, error_msg, duration_ms, created_at
		 FROM audit_log ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		opts.Limit, opts.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	entries, err := scanAuditEntries(rows)
	return entries, total, err
}

func scanAuditEntries(rows pgx.Rows) ([]domain.AuditEntry, error) {
	var entries []domain.AuditEntry
	for rows.Next() {
		var e domain.AuditEntry
		var userID, projectID, errorCode, errorMsg *string
		var scope []byte
		var createdAt time.Time

		err := rows.Scan(&e.ID, &userID, &projectID, &e.Operation, &scope, &e.Status, &errorCode, &errorMsg, &e.DurationMs, &createdAt)
		if err != nil {
			return nil, err
		}

		if userID != nil {
			e.UserID = *userID
		}
		if projectID != nil {
			e.ProjectID = *projectID
		}
		if errorCode != nil {
			e.ErrorCode = *errorCode
		}
		if errorMsg != nil {
			e.ErrorMsg = *errorMsg
		}
		if scope != nil {
			var s domain.OpScope
			json.Unmarshal(scope, &s)
			e.Scope = &s
		}
		e.CreatedAt = createdAt
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func normalizeOpts(opts domain.PaginationOpts) domain.PaginationOpts {
	if opts.Limit <= 0 || opts.Limit > 100 {
		opts.Limit = 50
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	return opts
}
