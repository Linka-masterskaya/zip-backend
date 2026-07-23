package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/domain"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
)

const (
	passwordResetTokenBytes   = 32
	passwordResetTokenPurpose = "password_reset"
)

// ForgotPassword создает и отправляет reset-токен, если email существует.
func (au *authService) ForgotPassword(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		return err
	}

	emailHash := au.crp.Hash([]byte(email))

	user, err := au.repo.GetUserByEmailHash(ctx, emailHash)
	if err != nil {
		if errors.Is(err, apperr.ErrUserNotFound) {
			return nil
		}
		return err
	}

	token, err := au.repo.CreatePasswordResetToken(ctx, user.ID, au.cfg.ResetPasswordTokenTTL)
	if err != nil {
		return err
	}

	// Письмо отправляется асинхронно, чтобы ответ 202 не зависел от SMTP.
	// WithoutCancel сохраняет context values для логов, но не отменяется после завершения HTTP-запроса,
	// поэтому SMTP-отправка может завершиться уже после ответа клиенту.
	go au.sendPasswordResetEmail(context.WithoutCancel(ctx), user.ID, email, token)

	return nil
}

func (au *authService) sendPasswordResetEmail(ctx context.Context, userID, email, token string) {
	if au.mailer == nil {
		slog.ErrorContext(ctx, "password reset email sender is not configured", "user_id", userID)
		return
	}

	if err := au.mailer.Send(ctx, email, domain.PasswordReset, domain.EmailData{
		Token: token,
		Email: email,
	}); err != nil {
		slog.ErrorContext(ctx, "password reset email send failed",
			"user_id", userID,
			logger.Err(err),
		)
	}
}

// ResetPassword использует reset-токен, меняет пароль и отзывает сессии.
func (au *authService) ResetPassword(ctx context.Context, token string, newPassword string) error {
	if strings.TrimSpace(token) == "" {
		return apperr.ErrInvalidResetToken
	}
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}

	passwordHash, err := hashPassword(newPassword, au.cfg.BcryptCost)
	if err != nil {
		return err
	}

	userID, err := au.repo.ResetPasswordByToken(ctx, token, passwordHash)
	if err != nil {
		if errors.Is(err, apperr.ErrInvalidResetToken) {
			return apperr.ErrInvalidResetToken
		}
		return err
	}

	if err := au.cache.RevokeAllSessions(ctx, userID); err != nil {
		slog.ErrorContext(ctx, "password reset sessions revoke failed",
			"user_id", userID,
			logger.Err(err),
		)
		return apperr.ErrInternal.WithError(err)
	}

	return nil
}

func hashPassword(password string, cost int) (string, error) {
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func newPasswordResetToken() (string, []byte, error) {
	rawToken := make([]byte, passwordResetTokenBytes)
	if _, err := rand.Read(rawToken); err != nil {
		return "", nil, fmt.Errorf("generate password reset token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(rawToken), rawToken, nil
}

func decodePasswordResetToken(token string) ([]byte, error) {
	rawToken, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, apperr.ErrInvalidResetToken
	}
	if len(rawToken) != passwordResetTokenBytes {
		return nil, apperr.ErrInvalidResetToken
	}
	return rawToken, nil
}

func hashPasswordResetToken(rawToken []byte) []byte {
	sum := sha256.Sum256(rawToken)
	return sum[:]
}
