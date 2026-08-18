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

type CreateOrganizationParams struct {
	ID   uuid.UUID
	Name string
}

type CreateUserParams struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
}

type CreateAuthCredParams struct {
	UserID         uuid.UUID
	EmailHash      []byte
	EmailEncrypted []byte
	PasswordHash   string
	Role           string
}

type CreateVerifyTokenParams struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash []byte
	ExpiresAt time.Time
	Purpose   string
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
			AND u.deleted_at IS NULL
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

func (r *authRepo) GetUserByID(ctx context.Context, userID uuid.UUID) (*User, error) {
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
		WHERE u.id = $1
			AND u.deleted_at IS NULL
	`

	err := r.db.QueryRow(ctx, query, userID).Scan(
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
		return nil, fmt.Errorf("authRepo.GetUserByID: %w", err)
	}

	return &user, nil
}

// CreatePasswordResetToken гасит активные не истекшие reset-токены пользователя,
// создает новый одноразовый reset-токен и сохраняет в БД его hash.
func (r *authRepo) CreatePasswordResetToken(
	ctx context.Context,
	userID string,
	ttl time.Duration,
) (string, error) {
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

	// Переход по ссылке из письма доказывает владение адресом ровно так же,
	// как и verify-флоу. Без этого владелец захваченного адреса сбрасывает
	// пароль, но всё равно упирается в 403 на входе.
	if _, err = tx.Exec(ctx, `
		UPDATE users
		SET email_verified = true,
		    updated_at = now()
		WHERE id = $1
		  AND email_verified = false
		  AND deleted_at IS NULL
	`, userID); err != nil {
		return "", fmt.Errorf("authRepo.ResetPasswordByToken verify email: %w", err)
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

func isEmailHashUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "auth_cred_email_hash_uniq"
}

func (r *authRepo) CreateOrganization(ctx context.Context, params CreateOrganizationParams) error {
	query := `
	INSERT INTO organizations(
	id,
	name)
	VALUES(
	$1,
	$2
	)`

	_, err := r.db.Exec(ctx, query, params.ID, params.Name)
	return err
}

func (r *authRepo) CreateUser(ctx context.Context, params CreateUserParams) error {
	query := `
	INSERT INTO users(
	id,
	org_id)
	VALUES(
	$1,
	$2
	)`

	_, err := r.db.Exec(ctx, query, params.ID, params.OrganizationID)
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

	_, err := r.db.Exec(
		ctx,
		query,
		params.UserID,
		params.EmailHash,
		params.EmailEncrypted,
		params.PasswordHash,
		params.Role,
	)

	if err != nil {
		if isEmailHashUniqueViolation(err) {
			return apperr.ErrConflict.WithMessage("email already exists")
		}

		return fmt.Errorf("authRepo.CreateAuthCred: %w", err)
	}
	return nil

}

func (r *authRepo) CreateVerifyToken(ctx context.Context, params CreateVerifyTokenParams) error {
	query := `
 INSERT INTO verify_tokens (
 id,
 user_id,
 token_hash,
 expires_at,
 purpose
 )
 VALUES (
 $1,
 $2,
 $3,
 $4,
 $5)`

	_, err := r.db.Exec(
		ctx,
		query,
		params.ID,
		params.UserID,
		params.TokenHash,
		params.ExpiresAt,
		params.Purpose,
	)

	return err
}

// replaceUnverifiedPassword перезаписывает пароль неподтверждённой регистрации.
// Пока адрес не подтверждён, владение им не доказала ни одна сторона, поэтому
// повторная регистрация на тот же адрес имеет право забрать его себе.
func (r *authRepo) replaceUnverifiedPassword(
	ctx context.Context,
	userID uuid.UUID,
	passwordHash string,
) error {
	res, err := r.db.Exec(ctx, `
		UPDATE auth_cred AS c
		SET password_hash = $1,
		    updated_at = now()
		FROM users AS u
		WHERE c.user_id = $2
		  AND u.id = c.user_id
		  AND u.email_verified = false
		  AND u.deleted_at IS NULL
	`, passwordHash, userID)
	if err != nil {
		return fmt.Errorf("authRepo.replaceUnverifiedPassword: %w", err)
	}
	if res.RowsAffected() == 0 {
		return apperr.ErrConflict.WithMessage("email already exists")
	}
	return nil
}

// DeleteStaleUnverifiedUsers удаляет регистрации, которые так и не подтвердили
// адрес: пока он занят, настоящий владелец не может им пользоваться.
//
// Пользователи, успевшие что-то создать, не трогаются: packs, folders,
// students, media_files и pack_versions ссылаются на users без каскада, и
// фоновая задача не должна удалять данные. В проде такие строки появиться не
// могут — неподтверждённый пользователь не проходит вход.
func (r *authRepo) DeleteStaleUnverifiedUsers(ctx context.Context, cutoff time.Time) (int64, error) {
	tx, err := r.beginTx(ctx)
	if err != nil {
		return 0, fmt.Errorf("authRepo.DeleteStaleUnverifiedUsers: begin tx: %w", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.ErrorContext(ctx, "tx rollback failed", logger.Err(err))
		}
	}()

	res, err := tx.Exec(ctx, `
		DELETE FROM users AS u
		WHERE u.email_verified = false
		  AND u.created_at < $1
		  AND NOT EXISTS (SELECT 1 FROM folders     f WHERE f.owner_id = u.id)
		  AND NOT EXISTS (SELECT 1 FROM packs       p WHERE p.owner_id = u.id)
		  AND NOT EXISTS (SELECT 1 FROM pack_versions v WHERE v.created_by = u.id)
		  AND NOT EXISTS (SELECT 1 FROM students    s WHERE s.defectologist_id = u.id)
		  AND NOT EXISTS (SELECT 1 FROM media_files m WHERE m.uploader_id = u.id)
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("authRepo.DeleteStaleUnverifiedUsers: delete users: %w", err)
	}

	// Личная организация остаётся без владельца — убираем и её, но только если
	// в ней действительно не осталось пользователей.
	if _, err = tx.Exec(ctx, `
		DELETE FROM organizations AS o
		WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.org_id = o.id)
	`); err != nil {
		return 0, fmt.Errorf("authRepo.DeleteStaleUnverifiedUsers: delete orgs: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("authRepo.DeleteStaleUnverifiedUsers: commit: %w", err)
	}

	return res.RowsAffected(), nil
}
