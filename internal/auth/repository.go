package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
)

type User struct {
	ID            string
	OrgID         *string
	PasswordHash  *string
	Role          string
	EmailVerified bool
}

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

func (r *authRepo) GetUserByEmailHash(ctx context.Context, emailHash []byte) (*User, error) {
	var user User

	query := `
		SELECT
			u.id,
			u.org_id,
			ac.password_hash,
			ac.role,
			u.email_verified
		FROM users u
		JOIN auth_cred ac ON ac.user_id = u.id
		WHERE ac.email_hash = $1
	`

	err := r.db.QueryRow(ctx, query, emailHash).Scan(
		&user.ID,
		&user.OrgID,
		&user.PasswordHash,
		&user.Role,
		&user.EmailVerified,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authRepo.GetUserByEmailHash: %w", err)
	}

	return &user, nil
}

// CreatePasswordResetToken гасит активные не истекшие reset-токены пользователя,
// создает новый одноразовый reset-токен и сохраняет в БД его hash.
func (r *authRepo) CreatePasswordResetToken(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	token, rawToken, err := newPasswordResetToken()
	if err != nil {
		return "", err
	}

	tx, err := r.beginTx(ctx)
	if err != nil {
		return "", fmt.Errorf("authRepo.CreatePasswordResetToken: %w", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.ErrorContext(ctx, "tx rollback failed", logger.Err(err))
		}
	}()

	_, err = tx.Exec(ctx, `
		UPDATE verify_tokens
		SET used_at = now()
		WHERE user_id = $1
		  AND purpose = $2
		  AND used_at IS NULL
		  AND expires_at > now()
	`, userID, passwordResetTokenPurpose)
	if err != nil {
		return "", fmt.Errorf("authRepo.CreatePasswordResetToken burn old tokens: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO verify_tokens (
			id,
			user_id,
			purpose,
			token_hash,
			expires_at
		)
		VALUES ($1, $2, $3, $4, $5)
	`, uuid.New(), userID, passwordResetTokenPurpose, hashPasswordResetToken(rawToken), time.Now().Add(ttl))
	if err != nil {
		return "", fmt.Errorf("authRepo.CreatePasswordResetToken insert token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("authRepo.CreatePasswordResetToken commit: %w", err)
	}

	return token, nil
}

// ResetPasswordByToken меняет пароль по валидному reset-токену в одной транзакции.
func (r *authRepo) ResetPasswordByToken(ctx context.Context, token string, passwordHash string) (string, error) {
	rawToken, err := decodePasswordResetToken(token)
	if err != nil {
		return "", err
	}

	tx, err := r.beginTx(ctx)
	if err != nil {
		return "", fmt.Errorf("authRepo.ResetPasswordByToken: %w", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.ErrorContext(ctx, "tx rollback failed", logger.Err(err))
		}
	}()

	var userID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE verify_tokens
		SET used_at = now()
		WHERE token_hash = $1
		  AND purpose = $2
		  AND used_at IS NULL
		  AND expires_at > now()
		RETURNING user_id
	`, hashPasswordResetToken(rawToken), passwordResetTokenPurpose).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apperr.ErrInvalidResetToken
	}
	if err != nil {
		return "", fmt.Errorf("authRepo.ResetPasswordByToken consume token: %w", err)
	}

	res, err := tx.Exec(ctx, `
		UPDATE auth_cred
		SET password_hash = $1,
		    updated_at = now()
		WHERE user_id = $2
	`, passwordHash, userID)
	if err != nil {
		return "", fmt.Errorf("authRepo.ResetPasswordByToken update password: %w", err)
	}
	if res.RowsAffected() == 0 {
		return "", apperr.ErrInternal.WithMessage("password credentials not found")
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("authRepo.ResetPasswordByToken commit: %w", err)
	}

	return userID.String(), nil
}

func (r *authRepo) withTx(tx pgx.Tx) authRepoIface {
	return &authRepo{
		db:   tx,
		pool: nil,
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

func (r *authRepo) useEmailVerifyToken(
	ctx context.Context,
	token []byte,
) (uuid.UUID, uuid.UUID, error) {
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
		WHERE id = $1
			AND email_verified = false
			AND deleted_at IS NULL
	`

	res, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("authRepo.verifyUser: %w", err)
	}
	if res.RowsAffected() == 0 {
		return apperr.ErrVerifyTokenInvalid
	}

	_, err = r.db.Exec(
		ctx,
		`UPDATE verify_tokens
		 SET used_at = now()
		 WHERE user_id = $1
		   AND purpose = 'email_verify'
		   AND used_at IS NULL`,
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
		WHERE id = $1
			AND email_verified = false
			AND deleted_at IS NULL
	`

	res, err := r.db.Exec(ctx, query, studentID)
	if err != nil {
		return fmt.Errorf("authRepo.verifyStudent: %w", err)
	}
	if res.RowsAffected() == 0 {
		return apperr.ErrVerifyTokenInvalid
	}

	_, err = r.db.Exec(
		ctx,
		`UPDATE verify_tokens
		 SET used_at = now()
		 WHERE student_id = $1
		   AND purpose = 'email_verify'
		   AND used_at IS NULL`,
		studentID,
	)
	if err != nil {
		return fmt.Errorf("authRepo.verifyStudent burn tokens: %w", err)
	}

	return nil
}

func (r *authRepo) rotateEmailTokens(
	ctx context.Context,
	tokenID, userID uuid.UUID,
	tokenHash []byte,
	expiresAt time.Time,
) error {
	query := `
		WITH invalidated AS (
			UPDATE verify_tokens
			SET used_at = now()
			WHERE user_id = $1
				AND used_at IS NULL
				AND purpose = 'email_verify'
			RETURNING 1
		)
		INSERT INTO verify_tokens (
			id,
			user_id,
			token_hash,
			expires_at,
			purpose
		)
		VALUES ($2, $1, $3, $4, 'email_verify')
	`

	_, err := r.db.Exec(ctx, query, userID, tokenID, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("authRepo.rotateEmailTokens: %w", err)
	}

	return nil
}

func (r *authRepo) getUserContactForResend(
	ctx context.Context,
	userID uuid.UUID,
) ([]byte, bool, error) {
	var (
		emailEncrypted []byte
		emailVerified  bool
	)

	err := r.db.QueryRow(
		ctx,
		`SELECT ac.email_encrypted, u.email_verified
		 FROM users u
		 JOIN auth_cred ac ON ac.user_id = u.id
		 WHERE u.id = $1
		   AND u.deleted_at IS NULL`,
		userID,
	).Scan(&emailEncrypted, &emailVerified)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, apperr.ErrUserNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("authRepo.getUserContactForResend: %w", err)
	}

	return emailEncrypted, emailVerified, nil
}
