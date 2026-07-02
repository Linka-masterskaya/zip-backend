package auth

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type authRepo struct {
	db   DBTX // внутренний интерфейс
	pool *pgxpool.Pool
}

func NewAuthRepo(pool *pgxpool.Pool) *authRepo {
	return &authRepo{db: pool, pool: pool}
}

// доп метод — возвращает копию с tx
func (r *authRepo) withTx(tx pgx.Tx) *authRepo {
	return &authRepo{db: tx, pool: r.pool}
}

func (r *authRepo) EmailExists(ctx context.Context, emailHash []byte) (bool, error) {
	var exists bool

	query := `
	SELECT EXISTS(
	SELECT *
	FROM auth_cred
	WHERE email_hash = $1)
	`
	err := r.db.QueryRow(ctx, query, emailHash).Scan(&exists)

	if err != nil {
		return false, err
	}

	return exists, nil
}

func (r *authRepo) CreateUser(ctx context.Context, params CreateUserParams) error {
	query := `
	INSERT INTO users(
	id, 
	organization_id,
	name)
	VALUES(
	$1,
	$2,
	$3
	)`

	_, err := r.db.Exec(ctx, query, params.ID, params.OrganizationID, params.Name)

	return err
}

func (r *authRepo) CreateAuthCred(ctx context.Context, params CreateAuthCredParams) error {
	query := `
INSERT INTO auth_cred (
user_id, 
email_hash,
email_encrypted,
password_hash, 
role)
VALUES (
$1,
$2,
$3,
$4,
$5)`

	_, err := r.db.Exec(ctx, query, params.UserID, params.EmailHash, params.EmailEncrypted, params.PasswordHash, params.Role)

	return err
}

func (r *authRepo) CreateVerifyToken(ctx context.Context, params CreateVerifyTokenParams) error {
	query := `
 INSERT INTO verify_tokens (
 id, 
 user_id,
 token_hash,
 expires_at
 )
 VALUES (
 $1,
 $2,
 $3,
 $4)`

	_, err := r.db.Exec(ctx, query, params.ID, params.UserID, params.TokenHash, params.ExpiresAt)

	return err
}
