package auth

import (
	"net/mail"
	"unicode/utf8"
)

func (r RegisterRequest) Validate() error {
	passwordLen := utf8.RuneCountInString(r.Password)

	if passwordLen < 8 || passwordLen > 72 {
		return ErrWeakPassword
	}

	if _, err := mail.ParseAddress(r.Email); err != nil {
		return ErrInvalidEmail
	}

	return nil
}
