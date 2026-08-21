package profile

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

const (
	avatarURLTTL = 15 * time.Minute
)

// CryptoService defines interface for encryption operations.
type CryptoService interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
	Hash(data []byte) []byte
}

// ObjectStorage is the MinIO subset required by profile avatars.
type ObjectStorage interface {
	PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	RemoveObject(ctx context.Context, key string) error
	ObjectSize(ctx context.Context, key string) (int64, error)
	PresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// RepoInterface defines all repository methods needed by the service.
type RepoInterface interface {
	GetUserProfile(ctx context.Context, userID uuid.UUID) (*UserProfile, error)
	UpdateDisplayName(ctx context.Context, userID uuid.UUID, displayName string) error
	AvatarState(ctx context.Context, userID string) (AvatarState, error)
	ReplaceAvatar(ctx context.Context, userID, expectedOldKey, newKey string, oldSize, storageDelta int64) (AvatarChange, error)
	ClearAvatar(ctx context.Context, userID, expectedOldKey string, oldSize int64) (AvatarChange, error)
	RestoreAvatarIfEmpty(ctx context.Context, userID string, oldKey string, oldSize int64) (bool, error)
	AddOrgStorageUsage(ctx context.Context, orgID string, delta int64) error
	CurrentAvatarKey(ctx context.Context, userID string) (string, error)
	SoftDeleteUser(ctx context.Context, userID string) (AvatarChange, error)
	ClaimAvatarCleanupJob(ctx context.Context, objectKey string) (*AvatarCleanupJob, error)
	SetAvatarCleanupSize(ctx context.Context, jobID int64, size int64) error
	AdjustAvatarCleanupQuota(ctx context.Context, jobID int64, size int64) error
	CompleteAvatarCleanup(ctx context.Context, jobID int64) error
	RetryAvatarCleanup(ctx context.Context, jobID int64, cause error) error

	FindByID(ctx context.Context, id uuid.UUID) (*User, error)
	FindByEmailHash(ctx context.Context, emailHash []byte) (*User, error)

	CreateToken(ctx context.Context, token *Token) error
	FindTokenByHash(ctx context.Context, hash []byte) (*Token, error)
	MarkTokenUsed(ctx context.Context, id string) error
	DeleteToken(ctx context.Context, id string) error
	DeleteExpiredTokens(ctx context.Context) (int64, error)

	BeginTx(ctx context.Context) (pgx.Tx, error)
	FindByIDWithTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*User, error)
	FindByEmailHashWithTx(ctx context.Context, tx pgx.Tx, emailHash []byte) (*User, error)
	UpdateEmailWithTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, emailEncrypted []byte, emailHash []byte, emailVerified bool) error
	MarkTokenUsedWithTx(ctx context.Context, tx pgx.Tx, id string) error
}

// AccountSessionManager extends bulk session revocation with a deletion barrier
// that serializes new login session creation against account soft-delete.
type AccountSessionManager interface {
	SessionRevoker
	DisableUserSessions(ctx context.Context, userID string) error
	EnableUserSessions(ctx context.Context, userID string) error
}

// Service contains profile, avatar, and email business logic.
type Service struct {
	repo     RepoInterface
	storage  ObjectStorage
	mailer   EmailSender
	crypto   CryptoService
	sessions AccountSessionManager
	emailCfg EmailConfig
}

// Response struct of profile response.
type Response struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	DisplayName   *string   `json:"display_name"`
	AvatarURL     *string   `json:"avatar_url"`
	Role          string    `json:"role"`
	EmailVerified bool      `json:"email_verified"`
	OrgID         *string   `json:"org_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// EmailConfig holds configuration for the email service.
type EmailConfig struct {
	EmailChangeTTL time.Duration
	EmailVerifyTTL time.Duration
}

// NewService creates profile service.
func NewService(
	repo RepoInterface,
	storageClient ObjectStorage,
	mailer EmailSender,
	crypto CryptoService,
	sessions AccountSessionManager,
	emailCfg EmailConfig,
) *Service {
	return &Service{
		repo:     repo,
		storage:  storageClient,
		mailer:   mailer,
		crypto:   crypto,
		sessions: sessions,
		emailCfg: emailCfg,
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
