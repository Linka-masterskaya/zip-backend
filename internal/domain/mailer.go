package domain

import "context"

// EmailSender — interface for sending emails.
type EmailSender interface {
	Send(ctx context.Context, to string, tmpl string, data map[string]any) error
}

// Template — type of letter template.
type Template string

// Template constants.
const (
	EmailVerify   Template = "email_verify"
	PasswordReset Template = "password_reset"
	EmailChange   Template = "email_change"
)
