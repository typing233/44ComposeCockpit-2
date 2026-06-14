package store

import (
	"context"

	"github.com/composecockpit/server/internal/domain"
)

type ACLRepository interface {
	GetUserProjectRole(ctx context.Context, userID string, projectID string) (domain.Role, error)
	SetProjectACL(ctx context.Context, acl domain.ProjectACL) error
	ListByProject(ctx context.Context, projectID string) ([]domain.ProjectACL, error)
	ListByUser(ctx context.Context, userID string) ([]domain.ProjectACL, error)
	Delete(ctx context.Context, userID, projectID string) error
}

type aclRepo struct {
	db *DB
}

func NewACLRepository(db *DB) ACLRepository {
	return &aclRepo{db: db}
}

func (r *aclRepo) GetUserProjectRole(ctx context.Context, userID string, projectID string) (domain.Role, error) {
	var role domain.Role
	err := r.db.Pool.QueryRow(ctx,
		`SELECT role FROM project_acl WHERE user_id = $1 AND project_id = $2`,
		userID, projectID).Scan(&role)
	if err != nil {
		return "", nil
	}
	return role, nil
}

func (r *aclRepo) SetProjectACL(ctx context.Context, acl domain.ProjectACL) error {
	_, err := r.db.Pool.Exec(ctx,
		`INSERT INTO project_acl (user_id, project_id, role, granted_by, granted_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (user_id, project_id) DO UPDATE SET role = $3, granted_by = $4, granted_at = $5`,
		acl.UserID, acl.ProjectID, acl.Role, acl.GrantedBy, acl.GrantedAt)
	return err
}

func (r *aclRepo) ListByProject(ctx context.Context, projectID string) ([]domain.ProjectACL, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT user_id, project_id, role, granted_by, granted_at FROM project_acl WHERE project_id = $1`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var acls []domain.ProjectACL
	for rows.Next() {
		var a domain.ProjectACL
		if err := rows.Scan(&a.UserID, &a.ProjectID, &a.Role, &a.GrantedBy, &a.GrantedAt); err != nil {
			return nil, err
		}
		acls = append(acls, a)
	}
	return acls, rows.Err()
}

func (r *aclRepo) ListByUser(ctx context.Context, userID string) ([]domain.ProjectACL, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT user_id, project_id, role, granted_by, granted_at FROM project_acl WHERE user_id = $1`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var acls []domain.ProjectACL
	for rows.Next() {
		var a domain.ProjectACL
		if err := rows.Scan(&a.UserID, &a.ProjectID, &a.Role, &a.GrantedBy, &a.GrantedAt); err != nil {
			return nil, err
		}
		acls = append(acls, a)
	}
	return acls, rows.Err()
}

func (r *aclRepo) Delete(ctx context.Context, userID, projectID string) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM project_acl WHERE user_id = $1 AND project_id = $2`, userID, projectID)
	return err
}
