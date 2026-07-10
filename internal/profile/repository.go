package profile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUserNotFound = errors.New("user not found")
var ErrStorageQuotaExceeded = errors.New("organization storage quota exceeded")
var ErrAvatarChanged = errors.New("avatar changed concurrently")

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

type UserAvatar struct {
	OrgID     sql.NullString
	AvatarKey sql.NullString
}

type AvatarState struct {
	OrgID      sql.NullString
	AvatarKey  string
	UsedBytes  int64
	QuotaBytes int64
	HasOrg     bool
}

type AvatarChange struct {
	OldKey  string
	OldSize int64
	OrgID   sql.NullString
}

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

func (r *Repository) ReplaceAvatar(ctx context.Context, userID, expectedOldKey, newKey string, oldSize, storageDelta int64) (AvatarChange, error) {
	return r.changeAvatar(ctx, userID, expectedOldKey, &newKey, oldSize, storageDelta, true)
}

func (r *Repository) ClearAvatar(ctx context.Context, userID, expectedOldKey string, oldSize int64) (AvatarChange, error) {
	return r.changeAvatar(ctx, userID, expectedOldKey, nil, oldSize, -oldSize, false)
}

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

func rollbackAvatarTx(ctx context.Context, tx pgx.Tx, operation string) {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Warn("rollback avatar transaction", "operation", operation, "err", err)
	}
}

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

func nullStringValue(value sql.NullString) string {
	result := ""
	if value.Valid {
		result = value.String
	}
	return result
}

func nullInt64Value(value sql.NullInt64) int64 {
	result := int64(0)
	if value.Valid {
		result = value.Int64
	}
	return result
}
