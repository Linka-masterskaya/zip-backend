package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type authRepo struct {
	db   DBTX
	pool *pgxpool.Pool
}

func NewAuthRepo(pool *pgxpool.Pool) authRepoIface {
	return &authRepo{
		db:   pool,
		pool: pool,
	}
}

func (r *authRepo) withTx(tx pgx.Tx) authRepoIface {
	return &authRepo{
		db:   tx,
		pool: nil, // чтобы никто не мог вызвать еще раз в текущей tx
	}
}

func (r *authRepo) beginTx(ctx context.Context) (pgx.Tx, error) {
	if r.pool == nil {
		return nil, fmt.Errorf("authRepo.beginTx: nested transaction attempted")
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("authRepo.beginTx: %w", err)
	}

	return tx, nil
}

func (r *authRepo) useEmailVerifyToken(ctx context.Context, token []byte) (uuid.UUID, uuid.UUID, error) {
	query := `
		UPDATE verify_tokens
		SET used_at = now()
		WHERE token_hash = $1
			AND purpose = 'email_verify'
			AND used_at IS NULL
			AND expires_at > now()
		RETURNING user_id, student_id
		`

	var userIDDB, studentIDDB pgtype.UUID
	err := r.db.QueryRow(ctx, query, token).Scan(&userIDDB, &studentIDDB)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, apperr.ErrVerifyTokenInvalid
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("authRepo.useEmailVerifyToken: %w", err)
	}

	var userID, studentID uuid.UUID
	if userIDDB.Valid {
		userID = uuid.UUID(userIDDB.Bytes)
	}
	if studentIDDB.Valid {
		studentID = uuid.UUID(studentIDDB.Bytes)
	}
	return userID, studentID, nil
}

func (r *authRepo) verifyUser(ctx context.Context, userID uuid.UUID) error {
	query := `
		UPDATE users
		SET email_verified = true
		WHERE id = $1 AND email_verified = false AND deleted_at IS NULL
		`

	res, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("authRepo.verifyUser: %w", err)
	}
	if res.RowsAffected() == 0 {
		return apperr.ErrVerifyTokenInvalid
	}

	_, err = r.db.Exec(ctx,
		`UPDATE verify_tokens SET used_at = now()
	 WHERE user_id = $1 AND purpose = 'email_verify' AND used_at IS NULL`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("authRepo.verifyUser burn tokens: %w", err)
	}

	return nil
}

func (r *authRepo) verifyStudent(ctx context.Context, studentID uuid.UUID) error {
	query := `
		UPDATE students
		SET email_verified = true
		WHERE id = $1 AND email_verified = false AND deleted_at IS NULL
		`

	res, err := r.db.Exec(ctx, query, studentID)
	if err != nil {
		return fmt.Errorf("authRepo.verifyStudent: %w", err)
	}
	if res.RowsAffected() == 0 {
		return apperr.ErrVerifyTokenInvalid
	}

	_, err = r.db.Exec(ctx,
		`UPDATE verify_tokens SET used_at = now()
	 WHERE student_id = $1 AND purpose = 'email_verify' AND used_at IS NULL`,
		studentID,
	)
	if err != nil {
		return fmt.Errorf("authRepo.verifyStudent burn tokens: %w", err)
	}

	return nil
}

func (r *authRepo) rotateEmailTokens(ctx context.Context, tokenID, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) error {
	query := `
		WITH invalidated AS (
			UPDATE verify_tokens
			SET used_at = now()
			WHERE user_id = $1 AND used_at IS NULL AND purpose = 'email_verify'
			RETURNING 1
	)
	INSERT INTO verify_tokens (id, user_id, token_hash, expires_at, purpose)
	VALUES ($2, $1, $3, $4, 'email_verify')
	`

	_, err := r.db.Exec(ctx, query, userID, tokenID, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("authRepo.rotateEmailTokens: %w", err)
	}

	return nil
}

func (r *authRepo) getUserContactForResend(ctx context.Context, userID uuid.UUID) ([]byte, bool, error) {
	var (
		emailEncrypted []byte
		emailVerified  bool
	)

	err := r.db.QueryRow(ctx,
		`SELECT c.email_encrypted, u.email_verified
		FROM users u
		JOIN auth_cred c ON c.user_id = u.id
		WHERE u.id = $1 AND u.deleted_at IS NULL`,
		userID).Scan(&emailEncrypted, &emailVerified)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, apperr.ErrUserNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("authRepo.getUserContactForResend: %w", err)
	}

	return emailEncrypted, emailVerified, nil
}
