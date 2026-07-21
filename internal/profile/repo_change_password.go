package profile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ChangePasswordRepo interface {
	Get(ctx context.Context, id uuid.UUID) (*UserPassword, error)
	Update(ctx context.Context, id uuid.UUID, newHash string) error
}

type changePasswordRepo struct {
	db *pgxpool.Pool
}

func NewChangePasswordRepo(db *pgxpool.Pool) ChangePasswordRepo {
	return &changePasswordRepo{db: db}
}

func (r *changePasswordRepo) Get(ctx context.Context, id uuid.UUID) (*UserPassword, error) {
	var hash sql.NullString
	err := r.db.QueryRow(ctx, `SELECT password_hash FROM auth_cred WHERE user_id = $1`, id).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("changePasswordRepo.Get: %w", err)
	}
	return &UserPassword{ID: id, Password: hash.String}, nil
}

func (r *changePasswordRepo) Update(ctx context.Context, id uuid.UUID, newHash string) error {
	res, err := r.db.Exec(ctx, `UPDATE auth_cred SET password_hash = $1, updated_at = now() WHERE user_id = $2`, newHash, id)
	if err != nil {
		return fmt.Errorf("changePasswordRepo.Update: %w", err)
	}
	affected := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
