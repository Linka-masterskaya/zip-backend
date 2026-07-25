package profile

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
)

const avatarURLTTL = 15 * time.Minute

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
	AvatarState(ctx context.Context, userID string) (AvatarState, error)
	ReplaceAvatar(ctx context.Context, userID, expectedOldKey, newKey string, oldSize, storageDelta int64) (AvatarChange, error)
	ClearAvatar(ctx context.Context, userID, expectedOldKey string, oldSize int64) (AvatarChange, error)
	RestoreAvatarIfEmpty(ctx context.Context, userID string, oldKey string, oldSize int64) (bool, error)
	AddOrgStorageUsage(ctx context.Context, orgID string, delta int64) error
	CurrentAvatarKey(ctx context.Context, userID string) (string, error)

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

// Service contains avatar and email business logic.
type Service struct {
	repo     RepoInterface
	storage  ObjectStorage
	mailer   EmailSender
	crypto   CryptoService
	sessions SessionRevoker
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
	sessions SessionRevoker,
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

// GetProfile retrieves the full user profile.
func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (*Response, error) {
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
	resp := &Response{
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

// encryptEmail encrypts email using crypto service.
func (s *Service) encryptEmail(email string) ([]byte, error) {
	return s.crypto.Encrypt([]byte(email))
}

// decryptEmail decrypts email using crypto service.
func (s *Service) decryptEmail(encrypted []byte) (string, error) {
	decrypted, err := s.crypto.Decrypt(encrypted)
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

// hashEmail hashes email using crypto service.
func (s *Service) hashEmail(email string) []byte {
	return s.crypto.Hash([]byte(email))
}

// ============ Avatar Methods ============

// ReplaceAvatar uploads a new avatar, persists its key and removes the old object.
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

// EmailChangePayload represents the payload for email change tokens.
type EmailChangePayload struct {
	NewEmail string `json:"new_email"`
	OldEmail string `json:"old_email"`
}

// generateEmailChangeToken generates a token for email change.
func (s *Service) generateEmailChangeToken(ctx context.Context, userID uuid.UUID, oldEmail, newEmail string) (*Token, error) {
	if userID == uuid.Nil || oldEmail == "" || newEmail == "" {
		return nil, fmt.Errorf("userID, oldEmail, and newEmail are required")
	}

	tokenRaw := make([]byte, 32)
	if _, err := rand.Read(tokenRaw); err != nil {
		return nil, fmt.Errorf("generate random token: %w", err)
	}

	tokenHash := sha256.Sum256(tokenRaw)
	tokenStr := base64.RawURLEncoding.EncodeToString(tokenRaw)

	payload := EmailChangePayload{
		NewEmail: newEmail,
		OldEmail: oldEmail,
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	token := &Token{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      TokenTypeEmailChange,
		Token:     tokenStr,
		TokenHash: tokenHash[:],
		Payload:   string(payloadJSON),
		Used:      false,
		ExpiresAt: time.Now().Add(s.emailCfg.EmailChangeTTL),
		CreatedAt: time.Now(),
	}

	if err := s.repo.CreateToken(ctx, token); err != nil {
		return nil, fmt.Errorf("save token: %w", err)
	}

	return token, nil
}

// generateEmailVerifyToken generates a token for email verification.
func (s *Service) generateEmailVerifyToken(ctx context.Context, userID uuid.UUID, email string) (*Token, error) {
	if userID == uuid.Nil || email == "" {
		return nil, fmt.Errorf("userID and email are required")
	}

	tokenRaw := make([]byte, 32)
	if _, err := rand.Read(tokenRaw); err != nil {
		return nil, fmt.Errorf("generate random token: %w", err)
	}

	tokenHash := sha256.Sum256(tokenRaw)
	tokenStr := base64.RawURLEncoding.EncodeToString(tokenRaw)

	payload := map[string]string{
		"email": email,
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	token := &Token{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      TokenTypeEmailVerify,
		Token:     tokenStr,
		TokenHash: tokenHash[:],
		Payload:   string(payloadJSON),
		Used:      false,
		ExpiresAt: time.Now().Add(s.emailCfg.EmailVerifyTTL),
		CreatedAt: time.Now(),
	}

	if err := s.repo.CreateToken(ctx, token); err != nil {
		return nil, fmt.Errorf("save token: %w", err)
	}

	return token, nil
}

// GenerateEmailChangeToken generates a token for email change without sending email.
func (s *Service) GenerateEmailChangeToken(ctx context.Context, userID uuid.UUID, newEmail string) (*Token, error) {
	if err := ValidateEmail(newEmail); err != nil {
		return nil, fmt.Errorf("invalid email: %w", err)
	}

	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, fmt.Errorf("user not found: %w", err)
		}
		return nil, fmt.Errorf("find user: %w", err)
	}

	email, err := s.decryptEmail(user.EmailEncrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypt email: %w", err)
	}
	user.Email = email

	if user.Email == newEmail {
		return nil, ErrEmailSameAsCurrent
	}

	emailHash := s.hashEmail(newEmail)
	existingUser, err := s.repo.FindByEmailHash(ctx, emailHash)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return nil, fmt.Errorf("check email availability: %w", err)
	}
	if existingUser != nil {
		return nil, fmt.Errorf("%w: %s", ErrEmailAlreadyUsed, newEmail)
	}

	token, err := s.generateEmailChangeToken(ctx, userID, user.Email, newEmail)
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	return token, nil
}

// SendEmailChangeConfirmation sends confirmation email with the token.
func (s *Service) SendEmailChangeConfirmation(ctx context.Context, userID uuid.UUID, token *Token) error {
	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return fmt.Errorf("user not found: %w", err)
		}
		return fmt.Errorf("find user: %w", err)
	}

	email, err := s.decryptEmail(user.EmailEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt email: %w", err)
	}
	user.Email = email

	var payload EmailChangePayload
	if err := json.Unmarshal([]byte(token.Payload), &payload); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}

	emailData := mailer.EmailData{
		Token:    token.Token,
		Username: user.Username,
		Email:    user.Email,
		NewEmail: payload.NewEmail,
	}

	if err := s.mailer.Send(ctx, user.Email, mailer.EmailChange, emailData); err != nil {
		return fmt.Errorf("send email: %w", err)
	}

	return nil
}

// RequestEmailChange handles the initial email change request.
func (s *Service) RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) error {
	newEmail = strings.TrimSpace(strings.ToLower(newEmail))
	token, err := s.GenerateEmailChangeToken(ctx, userID, newEmail)
	if err != nil {
		return err
	}

	if err := s.SendEmailChangeConfirmation(ctx, userID, token); err != nil {
		if delErr := s.repo.DeleteToken(ctx, token.ID); delErr != nil {
			slog.Error("failed to delete token after email failure",
				"error", delErr,
				"token_id", token.ID,
				"user_id", userID.String())
		}
		return err
	}

	return nil
}

// ConfirmEmailChange handles the email change confirmation.
func (s *Service) ConfirmEmailChange(ctx context.Context, tokenStr string) error {
	token, payload, err := s.validateEmailChangeToken(ctx, tokenStr)
	if err != nil {
		return err
	}

	if err := s.executeEmailChange(ctx, token, payload); err != nil {
		return err
	}

	if err := s.sendVerificationEmail(ctx, token.UserID, payload.NewEmail); err != nil {
		slog.Error("failed to send verification email to new address",
			"error", err,
			"user_id", token.UserID.String(),
			"new_email", payload.NewEmail)
	}

	if err := s.sessions.RevokeAllSessions(ctx, token.UserID.String()); err != nil {
		slog.Error("revoke all sessions after password change failed",
			"user_id", token.UserID.String(),
			logger.Err(err),
		)
	}

	return nil
}

// executeEmailChange performs the email change in a transaction.
func (s *Service) executeEmailChange(ctx context.Context, token *Token, payload *EmailChangePayload) error {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			slog.Error("rollback transaction failed", "error", rollbackErr)
		}
	}()

	user, err := s.repo.FindByIDWithTx(ctx, tx, token.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrTokenNotFound
		}
		return fmt.Errorf("find user: %w", err)
	}

	if err := s.validateEmailChange(ctx, tx, user, payload); err != nil {
		return err
	}

	emailEncrypted, err := s.encryptEmail(payload.NewEmail)
	if err != nil {
		return fmt.Errorf("encrypt email: %w", err)
	}
	emailHash := s.hashEmail(payload.NewEmail)

	if err := s.repo.UpdateEmailWithTx(ctx, tx, token.UserID, emailEncrypted, emailHash, false); err != nil {
		return fmt.Errorf("update email: %w", err)
	}

	if err := s.repo.MarkTokenUsedWithTx(ctx, tx, token.ID); err != nil {
		return fmt.Errorf("mark token used: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// validateEmailChange validates the email change request.
func (s *Service) validateEmailChange(ctx context.Context, tx pgx.Tx, user *User, payload *EmailChangePayload) error {
	email, err := s.decryptEmail(user.EmailEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt email: %w", err)
	}
	user.Email = email

	if user.Email != payload.OldEmail {
		return ErrEmailAlreadyChanged
	}

	emailHash := s.hashEmail(payload.NewEmail)
	existingUser, err := s.repo.FindByEmailHashWithTx(ctx, tx, emailHash)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return fmt.Errorf("check email availability: %w", err)
	}
	if existingUser != nil && existingUser.ID != user.ID {
		return fmt.Errorf("%w: %s", ErrEmailAlreadyUsed, payload.NewEmail)
	}

	return nil
}

// sendVerificationEmail sends verification email to the new address.
func (s *Service) sendVerificationEmail(ctx context.Context, userID uuid.UUID, newEmail string) error {
	verifyToken, err := s.generateEmailVerifyToken(ctx, userID, newEmail)
	if err != nil {
		slog.Error("failed to generate verification token",
			"error", err,
			"user_id", userID.String(),
			"new_email", newEmail)
		return err
	}

	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		slog.Error("failed to get user for verification email",
			"error", err,
			"user_id", userID.String())
		return err
	}

	email, err := s.decryptEmail(user.EmailEncrypted)
	if err != nil {
		slog.Error("failed to decrypt email for verification",
			"error", err,
			"user_id", userID.String())
		return err
	}
	user.Email = email

	verifyData := mailer.EmailData{
		Token:    verifyToken.Token,
		Username: user.Username,
		Email:    newEmail,
	}

	if err := s.mailer.Send(ctx, newEmail, mailer.EmailVerify, verifyData); err != nil {
		slog.Error("failed to send verification email to new address",
			"error", err,
			"user_id", userID.String(),
			"new_email", newEmail)
		return err
	}

	return nil
}

// validateEmailChangeToken validates and returns an email change token.
func (s *Service) validateEmailChangeToken(ctx context.Context, tokenStr string) (*Token, *EmailChangePayload, error) {
	tokenRaw, err := base64.RawURLEncoding.DecodeString(tokenStr)
	if err != nil {
		return nil, nil, ErrTokenInvalid
	}

	tokenHash := sha256.Sum256(tokenRaw)

	token, err := s.repo.FindTokenByHash(ctx, tokenHash[:])
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return nil, nil, ErrTokenNotFound
		}
		return nil, nil, err
	}

	if token.Type != TokenTypeEmailChange {
		return nil, nil, fmt.Errorf("invalid token type: expected email_change, got %s", token.Type)
	}

	if token.Used {
		return nil, nil, ErrTokenAlreadyUsed
	}

	if time.Now().After(token.ExpiresAt) {
		return nil, nil, ErrTokenExpired
	}

	var payload EmailChangePayload
	if err := json.Unmarshal([]byte(token.Payload), &payload); err != nil {
		return nil, nil, fmt.Errorf("parse payload: %w", err)
	}

	return token, &payload, nil
}

// DeleteExpiredTokens deletes all expired tokens.
func (s *Service) DeleteExpiredTokens(ctx context.Context) (int64, error) {
	count, err := s.repo.DeleteExpiredTokens(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete expired tokens: %w", err)
	}
	return count, nil
}

// ValidateEmail validates email format.
func ValidateEmail(email string) error {
	if email == "" {
		return fmt.Errorf("%w: email is empty", ErrEmailInvalid)
	}

	_, err := mail.ParseAddress(email)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrEmailInvalid, err.Error())
	}

	return nil
}
