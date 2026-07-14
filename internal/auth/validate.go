package auth

import (
	"net/mail"
	"strings"
)

func (r RegisterRequest) Validate() error {
	passwordLen := len(r.Password)

	if passwordLen < 8 || passwordLen > 72 {
		return ErrWeakPassword
	}

	email := strings.TrimSpace(r.Email)
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return ErrInvalidEmail
	}

	return nil
}
