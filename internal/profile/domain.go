package profile

import (
	"context"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/google/uuid"
)

type UserPassword struct {
	ID       uuid.UUID
	Password string
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
