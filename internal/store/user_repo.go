package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/composecockpit/server/internal/domain"
)

type UserRepository interface {
	Create(ctx context.Context, user *domain.User, password string) error
	GetByID(ctx context.Context, id string) (*domain.User, error)
	GetByUsername(ctx context.Context, username string) (*domain.User, error)
	List(ctx context.Context, opts domain.PaginationOpts) ([]domain.User, int, error)
	Update(ctx context.Context, user *domain.User) error
	Delete(ctx context.Context, id string) error
	ValidatePassword(ctx context.Context, username, password string) (*domain.User, error)
}

type userRepo struct {
	db         *DB
	bcryptCost int
}

func NewUserRepository(db *DB, bcryptCost int) UserRepository {
	return &userRepo{db: db, bcryptCost: bcryptCost}
}

func (r *userRepo) Create(ctx context.Context, user *domain.User, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), r.bcryptCost)
	if err != nil {
		return err
	}

	_, err = r.db.Pool.Exec(ctx,
		`INSERT INTO users (id, username, email, password_hash, role, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		user.ID, user.Username, user.Email, string(hash), user.Role, user.CreatedAt, user.UpdatedAt)
	return err
}

func (r *userRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	var u domain.User
	err := r.db.Pool.QueryRow(ctx,
		`SELECT id, username, email, password_hash, role, created_at, updated_at FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (r *userRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	var u domain.User
	err := r.db.Pool.QueryRow(ctx,
		`SELECT id, username, email, password_hash, role, created_at, updated_at FROM users WHERE username = $1`, username).
		Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (r *userRepo) List(ctx context.Context, opts domain.PaginationOpts) ([]domain.User, int, error) {
	opts = normalizeOpts(opts)

	var total int
	err := r.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := r.db.Pool.Query(ctx,
		`SELECT id, username, email, role, created_at, updated_at FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		opts.Limit, opts.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

func (r *userRepo) Update(ctx context.Context, user *domain.User) error {
	user.UpdatedAt = time.Now()
	_, err := r.db.Pool.Exec(ctx,
		`UPDATE users SET username=$1, email=$2, role=$3, updated_at=$4 WHERE id=$5`,
		user.Username, user.Email, user.Role, user.UpdatedAt, user.ID)
	return err
}

func (r *userRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}

func (r *userRepo) ValidatePassword(ctx context.Context, username, password string) (*domain.User, error) {
	user, err := r.GetByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, nil
	}
	return user, nil
}
