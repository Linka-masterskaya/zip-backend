package domain

import (
	"context"
)

// EmailSender — interface for sending emails.
type EmailSender interface {
	Send(ctx context.Context, to string, tmpl Template, data EmailData) error
}

// Template — type of letter template.
type Template string

// Template constants.
const (
	EmailVerify   Template = "email_verify"
	PasswordReset Template = "password_reset"
	EmailChange   Template = "email_change"
)

// EmailData - email data.
type EmailData struct {
	Token    string
	Username string
	Email    string
	NewEmail string
}
