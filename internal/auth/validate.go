package auth

import (
	"net/mail"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

func (r RegisterRequest) Validate() error {
	passwordLen := len(r.Password)

	if passwordLen < 8 || passwordLen > 72 {
		return apperr.ErrBadRequest.WithMessage("password must be 8-72 bytes long")
	}

	email := strings.TrimSpace(r.Email)
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return apperr.ErrBadRequest.WithMessage("invalid email")
	}

	return nil
}
