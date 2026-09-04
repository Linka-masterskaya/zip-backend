package mailer

import (
	"context"
	"io"
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
	AccountExists Template = "account_exists"
	PackShare     Template = "pack_share"
)

// Attachment is a streamed email attachment. The caller owns the reader and
// keeps it alive until Send returns.
type Attachment struct {
	Filename    string
	ContentType string
	Reader      io.Reader
}

// EmailData - email data.
type EmailData struct {
	Token       string
	Username    string
	Email       string
	NewEmail    string
	PackTitle   string
	Attachments []Attachment
}
