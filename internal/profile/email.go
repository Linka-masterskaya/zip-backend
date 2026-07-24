package profile

import (
	"context"
	"errors"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/google/uuid"
)

// Email-related errors.
var (
	ErrEmailInvalid        = errors.New("invalid email format")
	ErrEmailAlreadyUsed    = errors.New("email already in use by another user")
	ErrEmailSameAsCurrent  = errors.New("new email is the same as current email")
	ErrEmailAlreadyChanged = errors.New("email has already been changed")
)

// Token-related errors.
var (
	ErrTokenNotFound    = errors.New("token not found")
	ErrTokenInvalid     = errors.New("invalid token")
	ErrTokenExpired     = errors.New("token expired")
	ErrTokenAlreadyUsed = errors.New("token already used")
)

// EmailChangeRequest represents a request to change email.
type EmailChangeRequest struct {
	NewEmail string `json:"new_email"`
}

// EmailConfirmRequest represents a request to confirm email change.
type EmailConfirmRequest struct {
	Token string `json:"token"`
}

// User represents a user entity.
type User struct {
	ID             string    `json:"id"`
	Email          string    `json:"email"`
	EmailEncrypted []byte    `json:"-"` // Encrypted email for storage
	EmailVerified  bool      `json:"email_verified"`
	Username       string    `json:"username"`
	DisplayName    *string   `json:"display_name"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TokenType represents the type of token.
type TokenType string

// TokenType constants for different token purposes.
const (
	TokenTypeEmailChange TokenType = "email_change"
	TokenTypeEmailVerify TokenType = "email_verify"
)

// Token represents an authentication/verification token.
type Token struct {
	ID        string    `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Type      TokenType `json:"type"`
	Token     string    `json:"token"`   // Raw token for client
	TokenHash []byte    `json:"-"`       // Hashed token for storage
	Payload   string    `json:"payload"` // JSON string with additional data
	Used      bool      `json:"used"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// EmailSender — interface for sending emails.
type EmailSender interface {
	Send(ctx context.Context, to string, tmpl mailer.Template, data mailer.EmailData) error
}
