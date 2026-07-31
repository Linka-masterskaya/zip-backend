package profile

import "errors"

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
