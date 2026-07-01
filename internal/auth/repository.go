package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUserNotFound = errors.New("user not found")

type User struct {
	ID            string
	OrgID         *string
	Email         string
	PasswordHash  *string
	Role          string
	EmailVerified bool
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{
		pool: pool,
	}
}

func (r *Repository) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var user User
	query := `
	SELECT id, org_id, email, password_hash, role, email_verified
	FROM users
	WHERE email = $1 AND deleted_at IS NULL
	`
	row := r.pool.QueryRow(ctx, query, email)
	err := row.Scan(&user.ID, &user.OrgID, &user.Email, &user.PasswordHash, &user.Role, &user.EmailVerified)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return &user, nil
}
