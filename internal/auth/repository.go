package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUserNotFound = errors.New("user not found")

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
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authRepo.GetUserByEmailHash: %w", err)
	}

	return &user, nil
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

func (r *authRepo) GetUserByID(ctx context.Context, userID uuid.UUID) (*User, error) {
	query := `
		SELECT
			u.id,
			u.org_id,
			ac.password_hash,
			ac.role,
			u.email_verified
		FROM users u
		JOIN auth_cred ac ON ac.user_id = u.id
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`

	var user User
	var orgID *string

	err := r.db.QueryRow(ctx, query, userID).Scan(
		&user.ID,
		&orgID,
		&user.PasswordHash,
		&user.Role,
		&user.EmailVerified,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("authRepo.GetUserByID: %w", err)
	}

	user.OrgID = orgID
	return &user, nil
}

func (r *authRepo) GetAuthCredByUserID(ctx context.Context, userID uuid.UUID) (*UserCred, error) {
	query := `
		SELECT user_id, email_hash, email_encrypted, password_hash, role
		FROM auth_cred
		WHERE user_id = $1
	`

	var cred UserCred
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&cred.UserID,
		&cred.EmailHash,
		&cred.EmailEncrypted,
		&cred.PasswordHash,
		&cred.Role,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("authRepo.GetAuthCredByUserID: %w", err)
	}

	return &cred, nil
}

func (r *authRepo) FindIdentityByProviderUID(ctx context.Context, provider, providerUID string) (*UserIdentity, error) {
	query := `
		SELECT id, user_id, provider, provider_uid, created_at
		FROM auth_identities
		WHERE provider = $1 AND provider_uid = $2
	`

	var identity UserIdentity
	err := r.db.QueryRow(ctx, query, provider, providerUID).Scan(
		&identity.ID,
		&identity.UserID,
		&identity.Provider,
		&identity.ProviderUID,
		&identity.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("authRepo.FindIdentityByProviderUID: %w", err)
	}

	return &identity, nil
}

func (r *authRepo) CreateOAuthUser(ctx context.Context, params CreateUserParams) error {
	query := `
		INSERT INTO users (id, org_id, display_name, email_verified)
		VALUES ($1, $2, $3, $4)
	`

	var orgID any
	if params.OrganizationID == nil {
		orgID = nil
	} else {
		orgID = *params.OrganizationID
	}

	_, err := r.db.Exec(ctx, query, params.ID, orgID, params.Name, params.EmailVerified)
	if err != nil {
		return fmt.Errorf("authRepo.CreateOAuthUser: %w", err)
	}

	return nil
}

func (r *authRepo) CreateIdentity(ctx context.Context, identity *UserIdentity) error {
	query := `
        INSERT INTO auth_identities (id, user_id, provider, provider_uid)
        VALUES ($1, $2, $3, $4)
    `
	_, err := r.db.Exec(ctx, query,
		identity.ID,
		identity.UserID,
		identity.Provider,
		identity.ProviderUID,
	)
	if err != nil {
		return fmt.Errorf("authRepo.CreateIdentity: %w", err)
	}
	return nil
}

func (r *authRepo) CreateAuthCred(ctx context.Context, params CreateAuthCredParams) error {
	query := `
		INSERT INTO auth_cred (user_id, email_hash, email_encrypted, password_hash, role)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := r.db.Exec(ctx, query,
		params.UserID,
		params.EmailHash,
		params.EmailEncrypted,
		params.PasswordHash,
		params.Role,
	)
	if err != nil {
		return fmt.Errorf("authRepo.CreateAuthCred: %w", err)
	}

	return nil
}

func (r *authRepo) CreateOrganization(ctx context.Context, id uuid.UUID, name string) error {
	query := `INSERT INTO organizations (id, name) VALUES ($1, $2)`
	_, err := r.db.Exec(ctx, query, id, name)
	if err != nil {
		return fmt.Errorf("authRepo.CreateOrganization: %w", err)
	}
	return nil
}

func (r *authRepo) ResetPasswordByToken(ctx context.Context, token string, passwordHash string) (uuid.UUID, error) {
	rawToken, err := decodePasswordResetToken(token)
	if err != nil {
		return uuid.Nil, apperr.ErrInvalidResetToken
	}

	tokenHash := hashPasswordResetToken(rawToken)

	query := `
        UPDATE verify_tokens
        SET used_at = now()
        WHERE token_hash = $1
            AND purpose = 'password_reset'
            AND used_at IS NULL
            AND expires_at > now()
        RETURNING user_id
    `

	tx, err := r.beginTx(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("authRepo.ResetPasswordByToken: %w", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Error("tx rollback failed", "err", err)
		}
	}()

	var userID uuid.UUID
	err = tx.QueryRow(ctx, query, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, apperr.ErrInvalidResetToken
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("authRepo.ResetPasswordByToken: %w", err)
	}

	// Обновляем пароль
	updateQuery := `
        UPDATE auth_cred
        SET password_hash = $1, updated_at = now()
        WHERE user_id = $2
    `
	res, err := tx.Exec(ctx, updateQuery, passwordHash, userID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("authRepo.ResetPasswordByToken: %w", err)
	}
	if res.RowsAffected() == 0 {
		// Транзакция будет откачена через defer
		return uuid.Nil, apperr.ErrInternal.WithMessage("password credentials not found")
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("authRepo.ResetPasswordByToken: %w", err)
	}

	return userID, nil
}

func (r *authRepo) CreatePasswordResetToken(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	// Генерируем сырой токен
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return "", fmt.Errorf("CreatePasswordResetToken: generate token: %w", err)
	}

	// Кодируем токен для отправки пользователю
	token := base64.RawURLEncoding.EncodeToString(rawToken)

	// Хешируем токен для хранения в БД
	tokenHash := sha256.Sum256(rawToken)

	tokenID, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("CreatePasswordResetToken: generate id: %w", err)
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("CreatePasswordResetToken: parse user id: %w", err)
	}

	expiresAt := time.Now().Add(ttl)

	query := `
        INSERT INTO verify_tokens (id, user_id, purpose, token_hash, expires_at)
        VALUES ($1, $2, 'password_reset', $3, $4)
    `

	_, err = r.db.Exec(ctx, query, tokenID, userUUID, tokenHash[:], expiresAt)
	if err != nil {
		return "", fmt.Errorf("CreatePasswordResetToken: %w", err)
	}

	return token, nil
}

func (r *authRepo) DeleteStaleUnverifiedUsers(ctx context.Context, cutoff time.Time) (int64, error) {
	// Удаляем пользователей, у которых:
	// 1. Email не подтверждён (email_verified = false)
	// 2. Созданы раньше cutoff
	// 3. Нет активных verify_tokens (или все истекли/использованы)
	query := `
		WITH deleted_users AS (
			DELETE FROM users
			WHERE id IN (
				SELECT u.id
				FROM users u
				LEFT JOIN verify_tokens vt ON vt.user_id = u.id 
					AND vt.purpose = 'email_verify'
					AND vt.used_at IS NULL
					AND vt.expires_at > now()
				WHERE u.email_verified = false
					AND u.created_at < $1
					AND u.deleted_at IS NULL
					AND vt.id IS NULL
			)
			RETURNING id
		)
		SELECT COUNT(*) FROM deleted_users
	`

	var count int64
	err := r.db.QueryRow(ctx, query, cutoff).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("authRepo.DeleteStaleUnverifiedUsers: %w", err)
	}

	return count, nil
}

func (r *authRepo) InvalidateAllVerifyTokens(ctx context.Context, userID uuid.UUID) error {
	query := `
        UPDATE verify_tokens
        SET used_at = now()
        WHERE user_id = $1
            AND purpose = 'email_verify'
            AND used_at IS NULL
    `
	_, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("authRepo.InvalidateAllVerifyTokens: %w", err)
	}
	return nil
}
