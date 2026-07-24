package profile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository errors.
var (
	ErrUserNotFound         = errors.New("user not found")
	ErrStorageQuotaExceeded = errors.New("organization storage quota exceeded")
	ErrAvatarChanged        = errors.New("avatar changed concurrently")
)

// UserProfile represents the user data retrieved from the repository.
type UserProfile struct {
	ID             uuid.UUID
	EncryptedEmail []byte
	DisplayName    sql.NullString
	AvatarKey      sql.NullString
	Role           string
	EmailVerified  bool
	OrgID          sql.NullString
	CreatedAt      time.Time
}

// Repository handles database operations for profile.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new Repository instance.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// UserAvatar represents user avatar data from database.
type UserAvatar struct {
	OrgID     sql.NullString
	AvatarKey sql.NullString
}

// AvatarState represents current avatar state including storage usage.
type AvatarState struct {
	OrgID      sql.NullString
	AvatarKey  string
	UsedBytes  int64
	QuotaBytes int64
	HasOrg     bool
}

// AvatarChange represents changes made to avatar.
type AvatarChange struct {
	OldKey  string
	OldSize int64
	OrgID   sql.NullString
}

// GetUserProfile retrieves user data by ID, joining the users and auth_cred tables.
// Accepts userID as a string for consistency with the Avatar methods.
func (r *Repository) GetUserProfile(ctx context.Context, userID uuid.UUID) (*UserProfile, error) {
	var profile UserProfile
	err := r.db.QueryRow(ctx, `
		SELECT u.id, ac.email_encrypted, u.display_name, u.avatar_key, ac.role, u.email_verified, u.org_id::text, u.created_at
		FROM users u
		JOIN auth_cred ac ON u.id = ac.user_id
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, userID).Scan(
		&profile.ID, &profile.EncryptedEmail, &profile.DisplayName, &profile.AvatarKey, &profile.Role,
		&profile.EmailVerified, &profile.OrgID, &profile.CreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user profile: %w", err)
	}

	return &profile, nil
}

// AvatarState retrieves avatar state for a user.
func (r *Repository) AvatarState(ctx context.Context, userID string) (AvatarState, error) {
	var state AvatarState
	var avatarKey sql.NullString
	var usedBytes sql.NullInt64
	var quotaBytes sql.NullInt64
	err := r.db.QueryRow(ctx, `
		SELECT u.org_id::text, u.avatar_key, o.storage_used_bytes, o.storage_quota_bytes
		FROM users u
		LEFT JOIN organizations o ON o.id = u.org_id
		WHERE u.id = $1
	`, userID).Scan(&state.OrgID, &avatarKey, &usedBytes, &quotaBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return AvatarState{}, ErrUserNotFound
	}
	if err != nil {
		return AvatarState{}, fmt.Errorf("read avatar state: %w", err)
	}
	state.AvatarKey = nullStringValue(avatarKey)
	state.UsedBytes = nullInt64Value(usedBytes)
	state.QuotaBytes = nullInt64Value(quotaBytes)
	state.HasOrg = quotaBytes.Valid
	return state, nil
}

// ReplaceAvatar replaces user's avatar with a new one.
func (r *Repository) ReplaceAvatar(ctx context.Context, userID, expectedOldKey, newKey string, oldSize, storageDelta int64) (AvatarChange, error) {
	return r.changeAvatar(ctx, userID, expectedOldKey, &newKey, oldSize, storageDelta, true)
}

// ClearAvatar removes user's avatar.
func (r *Repository) ClearAvatar(ctx context.Context, userID, expectedOldKey string, oldSize int64) (AvatarChange, error) {
	return r.changeAvatar(ctx, userID, expectedOldKey, nil, oldSize, -oldSize, false)
}

// RestoreAvatarIfEmpty restores avatar if current key is empty.
func (r *Repository) RestoreAvatarIfEmpty(ctx context.Context, userID string, oldKey string, oldSize int64) (bool, error) {
	if oldKey == "" {
		return false, nil
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin restore avatar tx: %w", err)
	}
	defer rollbackAvatarTx(ctx, tx, "restore avatar")

	current, err := lockUserAvatar(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	if current.AvatarKey.Valid {
		return false, nil
	}
	if err = updateUserAvatar(ctx, tx, userID, &oldKey); err != nil {
		return false, err
	}
	if current.OrgID.Valid && oldSize != 0 {
		if err = updateOrgStorageUsage(ctx, tx, current.OrgID.String, oldSize, false); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit restore avatar tx: %w", err)
	}
	return true, nil
}

// CurrentAvatarKey returns current avatar key for a user.
func (r *Repository) CurrentAvatarKey(ctx context.Context, userID string) (string, error) {
	var avatarKey sql.NullString
	err := r.db.QueryRow(ctx, `SELECT avatar_key FROM users WHERE id = $1`, userID).Scan(&avatarKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read current avatar key: %w", err)
	}
	return nullStringValue(avatarKey), nil
}

// AddOrgStorageUsage adds delta to organization storage usage.
func (r *Repository) AddOrgStorageUsage(ctx context.Context, orgID string, delta int64) error {
	if orgID == "" || delta == 0 {
		return nil
	}
	_, err := r.db.Exec(ctx, `
		UPDATE organizations
		SET storage_used_bytes = GREATEST(storage_used_bytes + $2::bigint, 0::bigint)
		WHERE id = $1
	`, orgID, delta)
	if err != nil {
		return fmt.Errorf("compensate organization storage usage: %w", err)
	}
	return nil
}

// changeAvatar performs avatar change in a transaction.
func (r *Repository) changeAvatar(ctx context.Context, userID, expectedOldKey string, newKey *string, oldSize, storageDelta int64, enforceQuota bool) (AvatarChange, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return AvatarChange{}, fmt.Errorf("begin avatar tx: %w", err)
	}
	defer rollbackAvatarTx(ctx, tx, "change avatar")

	current, err := lockUserAvatar(ctx, tx, userID)
	if err != nil {
		return AvatarChange{}, err
	}
	if nullStringValue(current.AvatarKey) != expectedOldKey {
		return AvatarChange{}, ErrAvatarChanged
	}
	if err = updateUserAvatar(ctx, tx, userID, newKey); err != nil {
		return AvatarChange{}, err
	}
	if current.OrgID.Valid && storageDelta != 0 {
		if err = updateOrgStorageUsage(ctx, tx, current.OrgID.String, storageDelta, enforceQuota && storageDelta > 0); err != nil {
			return AvatarChange{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return AvatarChange{}, fmt.Errorf("commit avatar tx: %w", err)
	}
	return AvatarChange{OldKey: expectedOldKey, OldSize: oldSize, OrgID: current.OrgID}, nil
}

// rollbackAvatarTx rolls back avatar transaction with error logging.
func rollbackAvatarTx(ctx context.Context, tx pgx.Tx, operation string) {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Warn("rollback avatar transaction", "operation", operation, "err", err)
	}
}

// lockUserAvatar locks user row for avatar update.
func lockUserAvatar(ctx context.Context, tx pgx.Tx, userID string) (UserAvatar, error) {
	var avatar UserAvatar
	err := tx.QueryRow(ctx, `
		SELECT org_id::text, avatar_key FROM users WHERE id = $1 FOR UPDATE
	`, userID).Scan(&avatar.OrgID, &avatar.AvatarKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserAvatar{}, ErrUserNotFound
	}
	if err != nil {
		return UserAvatar{}, fmt.Errorf("lock user for avatar update: %w", err)
	}
	return avatar, nil
}

// updateUserAvatar updates user's avatar key.
func updateUserAvatar(ctx context.Context, tx pgx.Tx, userID string, newKey *string) error {
	var avatarKey any
	if newKey != nil {
		avatarKey = *newKey
	}
	_, err := tx.Exec(ctx, `UPDATE users SET avatar_key = $2, updated_at = now() WHERE id = $1`, userID, avatarKey)
	if err != nil {
		return fmt.Errorf("update user avatar key: %w", err)
	}
	return nil
}

// updateOrgStorageUsage updates organization storage usage.
func updateOrgStorageUsage(ctx context.Context, tx pgx.Tx, orgID string, delta int64, enforceQuota bool) error {
	commandTag, err := tx.Exec(ctx, `
		UPDATE organizations
		SET storage_used_bytes = GREATEST(storage_used_bytes + $2::bigint, 0::bigint)
		WHERE id = $1
		  AND (NOT $3::boolean OR storage_used_bytes + $2::bigint <= storage_quota_bytes)
	`, orgID, delta, enforceQuota)
	if err != nil {
		return fmt.Errorf("update organization storage usage: %w", err)
	}
	if enforceQuota && commandTag.RowsAffected() == 0 {
		return ErrStorageQuotaExceeded
	}
	return nil
}

// nullStringValue returns string from sql.NullString or empty string.
func nullStringValue(value sql.NullString) string {
	result := ""
	if value.Valid {
		result = value.String
	}
	return result
}

// nullInt64Value returns int64 from sql.NullInt64 or zero.
func nullInt64Value(value sql.NullInt64) int64 {
	result := int64(0)
	if value.Valid {
		result = value.Int64
	}
	return result
}

// ============ User Methods ============

// FindByID retrieves a user by UUID.
func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*User, error) {
	var user User
	var displayName sql.NullString
	var emailEncrypted []byte

	err := r.db.QueryRow(ctx, `
		SELECT u.id, ac.email_encrypted, u.email_verified, u.display_name, u.created_at, u.updated_at
		FROM users u
		LEFT JOIN auth_cred ac ON ac.user_id = u.id
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, id).Scan(
		&user.ID, &emailEncrypted,
		&user.EmailVerified, &displayName,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}

	user.EmailEncrypted = emailEncrypted

	if displayName.Valid {
		user.DisplayName = &displayName.String
	}
	user.Username = displayName.String

	return &user, nil
}

// FindByEmailHash retrieves a user by email hash.
func (r *Repository) FindByEmailHash(ctx context.Context, emailHash []byte) (*User, error) {
	var user User
	var displayName sql.NullString
	var emailEncrypted []byte

	err := r.db.QueryRow(ctx, `
		SELECT u.id, ac.email_encrypted, u.email_verified, u.display_name, u.created_at, u.updated_at
		FROM users u
		JOIN auth_cred ac ON ac.user_id = u.id
		WHERE ac.email_hash = $1 AND u.deleted_at IS NULL
	`, emailHash).Scan(
		&user.ID, &emailEncrypted,
		&user.EmailVerified, &displayName,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by email hash: %w", err)
	}

	user.EmailEncrypted = emailEncrypted

	if displayName.Valid {
		user.DisplayName = &displayName.String
	}
	user.Username = displayName.String

	return &user, nil
}

// ============ Token Methods ============

// CreateToken creates a new verification token.
func (r *Repository) CreateToken(ctx context.Context, token *Token) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO verify_tokens (id, user_id, purpose, token_hash, payload, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, token.ID, token.UserID, string(token.Type), token.TokenHash, token.Payload, token.ExpiresAt, token.CreatedAt)
	if err != nil {
		return fmt.Errorf("create token: %w", err)
	}
	return nil
}

// FindTokenByHash finds a token by its hashed value.
func (r *Repository) FindTokenByHash(ctx context.Context, hash []byte) (*Token, error) {
	var token Token
	var usedAt sql.NullTime
	var payload []byte

	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, purpose, payload, expires_at, used_at, created_at
		FROM verify_tokens
		WHERE token_hash = $1
	`, hash).Scan(
		&token.ID, &token.UserID,
		&token.Type, &payload,
		&token.ExpiresAt, &usedAt, &token.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find token by hash: %w", err)
	}

	token.Payload = string(payload)
	token.Used = usedAt.Valid

	return &token, nil
}

// MarkTokenUsed marks a token as used.
func (r *Repository) MarkTokenUsed(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE verify_tokens
		SET used_at = now()
		WHERE id = $1 AND used_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("mark token used: %w", err)
	}
	return nil
}

// DeleteToken deletes a token by ID.
func (r *Repository) DeleteToken(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM verify_tokens
		WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	return nil
}

// DeleteExpiredTokens deletes all expired tokens.
func (r *Repository) DeleteExpiredTokens(ctx context.Context) (int64, error) {
	result, err := r.db.Exec(ctx, `
		DELETE FROM verify_tokens
		WHERE expires_at < now()
	`)
	if err != nil {
		return 0, fmt.Errorf("delete expired tokens: %w", err)
	}
	return result.RowsAffected(), nil
}

// ============ Transaction Methods ============

// BeginTx starts a new database transaction.
func (r *Repository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return tx, nil
}

// FindByIDWithTx retrieves a user by UUID within a transaction.
func (r *Repository) FindByIDWithTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*User, error) {
	var user User
	var displayName sql.NullString
	var emailEncrypted []byte

	err := tx.QueryRow(ctx, `
		SELECT u.id, ac.email_encrypted, u.email_verified, u.display_name, u.created_at, u.updated_at
		FROM users u
		LEFT JOIN auth_cred ac ON ac.user_id = u.id
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, id).Scan(
		&user.ID, &emailEncrypted,
		&user.EmailVerified, &displayName,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id with tx: %w", err)
	}

	user.EmailEncrypted = emailEncrypted

	if displayName.Valid {
		user.DisplayName = &displayName.String
	}
	user.Username = displayName.String

	return &user, nil
}

// FindByEmailHashWithTx retrieves a user by email hash within a transaction.
func (r *Repository) FindByEmailHashWithTx(ctx context.Context, tx pgx.Tx, emailHash []byte) (*User, error) {
	var user User
	var displayName sql.NullString
	var emailEncrypted []byte

	err := tx.QueryRow(ctx, `
		SELECT u.id, ac.email_encrypted, u.email_verified, u.display_name, u.created_at, u.updated_at
		FROM users u
		JOIN auth_cred ac ON ac.user_id = u.id
		WHERE ac.email_hash = $1 AND u.deleted_at IS NULL
	`, emailHash).Scan(
		&user.ID, &emailEncrypted,
		&user.EmailVerified, &displayName,
		&user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by email hash with tx: %w", err)
	}

	user.EmailEncrypted = emailEncrypted

	if displayName.Valid {
		user.DisplayName = &displayName.String
	}
	user.Username = displayName.String

	return &user, nil
}

// UpdateEmailWithTx updates only the user's email and email_verified status within a transaction.
func (r *Repository) UpdateEmailWithTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, emailEncrypted []byte, emailHash []byte, emailVerified bool) error {
	_, err := tx.Exec(ctx, `
        UPDATE auth_cred
        SET email_encrypted = $2, email_hash = $3, updated_at = now()
        WHERE user_id = $1
    `, userID, emailEncrypted, emailHash)
	if err != nil {
		return fmt.Errorf("update auth_cred with tx: %w", err)
	}

	_, err = tx.Exec(ctx, `
        UPDATE users
        SET email_verified = $2, updated_at = now()
        WHERE id = $1 AND deleted_at IS NULL
    `, userID, emailVerified)
	if err != nil {
		return fmt.Errorf("update user with tx: %w", err)
	}

	return nil
}

// MarkTokenUsedWithTx marks a token as used within a transaction.
func (r *Repository) MarkTokenUsedWithTx(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `
		UPDATE verify_tokens
		SET used_at = now()
		WHERE id = $1 AND used_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("mark token used with tx: %w", err)
	}
	return nil
}
