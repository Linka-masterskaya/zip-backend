package auth

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

func ValidatePassword(password string) error {
	passwordLen := len(password)

	if passwordLen < 8 || passwordLen > 72 {
		return apperr.ErrBadRequest.WithMessage("password must be 8-72 bytes long")
	}

	return nil
}

func ValidateEmail(email string) error {
	email = strings.TrimSpace(email)

	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return apperr.ErrBadRequest.WithMessage("invalid email")
	}

	return nil
}

func ValidateName(userName string) error {
	userName = strings.TrimSpace(userName)
	lenName := utf8.RuneCountInString(userName)

	if lenName < 2 || lenName > 100 {
		return apperr.ErrBadRequest.WithMessage("name must be 2-100 characters long")
	}

	return nil
}
