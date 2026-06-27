package mailer

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/domain"
)

// Validations errors.
var (
	ErrEmptyTo         = errors.New("email recipient is required")
	ErrInvalidEmail    = errors.New("invalid email address")
	ErrEmptyToken      = errors.New("token is required")
	ErrEmptyNewEmail   = errors.New("new email is required")
	ErrInvalidNewEmail = errors.New("invalid new email address")
)

// validateEmail — verifies the correctness of the email.
func validateEmail(email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return ErrEmptyTo
	}

	_, err := mail.ParseAddress(email)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidEmail, email)
	}

	return nil
}

// validateToken — checks that the token is not empty.
func validateToken(token string) error {
	if strings.TrimSpace(token) == "" {
		return ErrEmptyToken
	}
	return nil
}

// SendVerifyEmail — sending a confirmation email.
func (s *SMTPSender) SendVerifyEmail(
	ctx context.Context,
	to string,
	token string,
	username string,
) error {
	if err := validateEmail(to); err != nil {
		return err
	}

	if err := validateToken(token); err != nil {
		return err
	}

	if username == "" {
		username = "Пользователь"
	}

	data := map[string]any{
		"Token":    token,
		"Username": username,
	}

	return s.Send(ctx, to, domain.EmailVerify, data)
}

// SendPasswordReset — sending a password reset email.
func (s *SMTPSender) SendPasswordReset(
	ctx context.Context,
	to string,
	token string,
) error {
	if err := validateEmail(to); err != nil {
		return err
	}

	if err := validateToken(token); err != nil {
		return err
	}

	data := map[string]any{
		"Token": token,
		"Email": to,
	}

	return s.Send(ctx, to, domain.PasswordReset, data)
}

// SendEmailChange — sending an email to confirm the email change.
func (s *SMTPSender) SendEmailChange(
	ctx context.Context,
	to string,
	token string,
	newEmail string,
	username string,
) error {
	if err := validateEmail(to); err != nil {
		return err
	}

	if err := validateEmail(newEmail); err != nil {
		return err
	}

	if strings.EqualFold(strings.TrimSpace(to), strings.TrimSpace(newEmail)) {
		err := errors.New("new email must be different from current email")
		return err
	}

	if err := validateToken(token); err != nil {
		return err
	}

	if username == "" {
		username = "Пользователь"
	}

	data := map[string]any{
		"Token":    token,
		"NewEmail": newEmail,
		"Username": username,
	}

	return s.Send(ctx, to, domain.EmailChange, data)
}
