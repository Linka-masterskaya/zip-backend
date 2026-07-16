package profile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
)

const avatarURLTTL = 15 * time.Minute

// ObjectStorage is the MinIO subset required by profile avatars.
type ObjectStorage interface {
	PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	RemoveObject(ctx context.Context, key string) error
	ObjectSize(ctx context.Context, key string) (int64, error)
	PresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// AvatarRepository is the persistence subset required by profile avatars.
type AvatarRepository interface {
	AvatarState(ctx context.Context, userID string) (AvatarState, error)
	ReplaceAvatar(ctx context.Context, userID, expectedOldKey, newKey string, oldSize, storageDelta int64) (AvatarChange, error)
	ClearAvatar(ctx context.Context, userID, expectedOldKey string, oldSize int64) (AvatarChange, error)
	RestoreAvatarIfEmpty(ctx context.Context, userID string, oldKey string, oldSize int64) (bool, error)
	AddOrgStorageUsage(ctx context.Context, orgID string, delta int64) error
	CurrentAvatarKey(ctx context.Context, userID string) (string, error)
}

// Service contains avatar business logic.
type Service struct {
	repo    ProfileRepository
	storage ObjectStorage
	crypto  *cryptox.Cryptox // Added a dependency for email decryption.
}

type ProfileResponse struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	DisplayName   *string   `json:"display_name"`
	AvatarURL     *string   `json:"avatar_url"`
	Role          string    `json:"role"`
	EmailVerified bool      `json:"email_verified"`
	OrgID         *string   `json:"org_id"`
	CreatedAt     time.Time `json:"created_at"`
}

type ProfileRepository interface {
	AvatarRepository
	GetUserProfile(ctx context.Context, userID uuid.UUID) (*UserProfile, error)
}

// NewService creates profile service.
// NewService has been updated to accept crypto.
func NewService(repo ProfileRepository, storageClient ObjectStorage, crypto *cryptox.Cryptox) *Service {
	return &Service{repo: repo, storage: storageClient, crypto: crypto}
}

// GetProfile retrieves the full user profile.
func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (*ProfileResponse, error) {
	// 1. get raw data
	user, err := s.repo.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, profileError(err)
	}

	// 2. decrypt email
	plainEmailBytes, err := s.crypto.Decrypt(user.EncryptedEmail)
	if err != nil {
		slog.Error("failed to decrypt user email", "user_id", userID, logger.Err(err))
		return nil, apperr.ErrInternal.WithMessage("failed to process user data")
	}

	// 3. build a base response
	resp := &ProfileResponse{
		ID:            user.ID.String(),
		Email:         string(plainEmailBytes),
		Role:          user.Role,
		EmailVerified: user.EmailVerified,
		CreatedAt:     user.CreatedAt,
	}

	// 4. extract nullable fields
	if user.DisplayName.Valid {
		resp.DisplayName = &user.DisplayName.String
	}
	if user.OrgID.Valid {
		resp.OrgID = &user.OrgID.String
	}

	// 5. generate a presigned URL only if the avatar exists
	if user.AvatarKey.Valid && user.AvatarKey.String != "" {
		avatarURL, err := s.storage.PresignedURL(ctx, user.AvatarKey.String, avatarURLTTL)
		if err != nil {
			slog.Warn("failed to generate presigned url for avatar", "key", user.AvatarKey.String, logger.Err(err))
			resp.AvatarURL = nil
		} else {
			resp.AvatarURL = &avatarURL
		}
	}
	return resp, nil
}

// ReplaceAvatar uploads a new avatar, persists its key and removes the old object.
// Storage operations are deliberately performed outside repository transactions.
func (s *Service) ReplaceAvatar(ctx context.Context, userID string, reader io.Reader, size int64, mimeType string) (string, error) {
	state, oldSize, err := s.avatarStateWithObjectSize(ctx, userID)
	if err != nil {
		return "", err
	}
	storageDelta := size - oldSize
	if err = validateStorageQuota(state, storageDelta); err != nil {
		return "", err
	}

	newKey := avatarKey(userID)
	if err = s.storage.PutObject(ctx, newKey, reader, size, mimeType); err != nil {
		return "", fmt.Errorf("put avatar object: %w", err)
	}

	avatarURL, err := s.storage.PresignedURL(ctx, newKey, avatarURLTTL)
	if err != nil {
		s.cleanupNewObject(ctx, newKey)
		return "", fmt.Errorf("generate avatar url: %w", err)
	}

	change, err := s.repo.ReplaceAvatar(ctx, userID, state.AvatarKey, newKey, oldSize, storageDelta)
	if err != nil {
		s.cleanupNewObject(ctx, newKey)
		return "", profileError(err)
	}
	if err = s.removeObject(ctx, change.OldKey); err != nil {
		s.compensateOldObjectUsage(ctx, change, err)
	}
	return s.currentAvatarURL(ctx, userID, newKey, avatarURL), nil
}

// DeleteAvatar clears the DB key and removes the object outside the DB transaction.
func (s *Service) DeleteAvatar(ctx context.Context, userID string) error {
	state, oldSize, err := s.avatarStateWithObjectSize(ctx, userID)
	if err != nil {
		return err
	}
	change, err := s.repo.ClearAvatar(ctx, userID, state.AvatarKey, oldSize)
	if err != nil {
		return profileError(err)
	}
	if err = s.removeObject(ctx, change.OldKey); err != nil {
		s.restoreDeletedAvatar(ctx, userID, change, err)
		return err
	}
	return nil
}

func (s *Service) avatarStateWithObjectSize(ctx context.Context, userID string) (AvatarState, int64, error) {
	state, err := s.repo.AvatarState(ctx, userID)
	if err != nil {
		return AvatarState{}, 0, profileError(err)
	}
	oldSize, err := s.objectSize(ctx, state.AvatarKey)
	if err != nil {
		return AvatarState{}, 0, err
	}
	return state, oldSize, nil
}

func validateStorageQuota(state AvatarState, storageDelta int64) error {
	if !state.HasOrg {
		return apperr.ErrForbidden.WithMessage("user organization is required for avatar upload")
	}
	if storageDelta > 0 && state.UsedBytes+storageDelta > state.QuotaBytes {
		return apperr.ErrForbidden.WithMessage("organization storage quota exceeded")
	}
	return nil
}

func (s *Service) currentAvatarURL(ctx context.Context, userID string, expectedKey string, fallbackURL string) string {
	currentKey, err := s.repo.CurrentAvatarKey(ctx, userID)
	if err != nil {
		slog.Warn("read current avatar key before response failed", "user_id", userID, "err", err)
		return fallbackURL
	}
	if currentKey == "" || currentKey == expectedKey {
		return fallbackURL
	}

	currentURL, err := s.storage.PresignedURL(ctx, currentKey, avatarURLTTL)
	if err != nil {
		slog.Warn("generate current avatar url before response failed", "key", currentKey, "err", err)
		return fallbackURL
	}
	return currentURL
}

func (s *Service) objectSize(ctx context.Context, key string) (int64, error) {
	var size int64
	if key != "" {
		objectSize, err := s.storage.ObjectSize(ctx, key)
		if errors.Is(err, storage.ErrObjectNotFound) {
			size = 0
		} else if err != nil {
			return 0, fmt.Errorf("stat avatar object: %w", err)
		} else {
			size = objectSize
		}
	}
	return size, nil
}

func (s *Service) removeObject(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	if err := s.storage.RemoveObject(ctx, key); err != nil && !errors.Is(err, storage.ErrObjectNotFound) {
		return fmt.Errorf("remove avatar object: %w", err)
	}
	return nil
}

func (s *Service) cleanupNewObject(ctx context.Context, key string) {
	if err := s.removeObject(ctx, key); err != nil {
		slog.Error("cleanup uploaded avatar after db error failed", "key", key, "err", err)
	}
}

func (s *Service) compensateOldObjectUsage(ctx context.Context, change AvatarChange, cause error) {
	slog.Error("old avatar object cleanup failed", "key", change.OldKey, "err", cause)
	if !change.OrgID.Valid || change.OldSize == 0 {
		return
	}
	if err := s.repo.AddOrgStorageUsage(ctx, change.OrgID.String, change.OldSize); err != nil {
		slog.Error("old avatar storage usage compensation failed",
			"key", change.OldKey,
			"old_size", change.OldSize,
			"err", err,
		)
	}
}

func (s *Service) restoreDeletedAvatar(ctx context.Context, userID string, change AvatarChange, cause error) {
	slog.Error("avatar object delete failed", "key", change.OldKey, "err", cause)
	restored, err := s.repo.RestoreAvatarIfEmpty(ctx, userID, change.OldKey, change.OldSize)
	if err != nil {
		slog.Error("restore avatar after delete failure failed", "key", change.OldKey, "err", err)
		s.compensateOldObjectUsage(ctx, change, cause)
		return
	}
	if !restored {
		s.compensateOldObjectUsage(ctx, change, cause)
	}
}

func profileError(err error) error {
	if errors.Is(err, ErrUserNotFound) {
		return apperr.ErrUnauthorized
	}
	if errors.Is(err, ErrStorageQuotaExceeded) {
		return apperr.ErrForbidden.WithMessage("organization storage quota exceeded")
	}
	if errors.Is(err, ErrAvatarChanged) {
		return apperr.ErrConflict.WithMessage("avatar changed concurrently; retry request")
	}
	return err
}

func avatarKey(userID string) string {
	return fmt.Sprintf("avatars/%s/%s", userID, uuid.New().String())
}
