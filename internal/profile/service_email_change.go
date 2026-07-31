package profile

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
)

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
