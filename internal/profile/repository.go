package profile

import (
	"context"
	"database/sql"
)

type UserRepo interface {
	Get(ctx context.Context, id string) (*UserPassword, error)
	Update(ctx context.Context, id string, newHash string) error
}
type userRepo struct {
	db *sql.DB
}

func NewUserRepo(db *sql.DB) UserRepo {
	return &userRepo{db: db}
}

func (r *userRepo) Get(ctx context.Context, id string) (*UserPassword, error) {
	var user UserPassword
	query := `SELECT id, password_hash FROM users WHERE id=$1`
	err := r.db.QueryRowContext(ctx, query, id).
		Scan(&user.ID, &user.Password)
	if err != nil {
		return nil, err
	}
	return &user, nil
}
func (r *userRepo) Update(ctx context.Context, id string, newHash string) error {
	query := `UPDATE users SET password_hash=$1 WHERE id=$2`
	_, err := r.db.ExecContext(ctx, query, newHash, id)
	if err != nil {
		return err
	}
	return err
}
