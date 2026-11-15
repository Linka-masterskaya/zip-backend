package auth

import (
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

const (
	minNameRunes      = 2
	maxNameRunes      = 100
	minEmailLen       = 6
	maxEmailLen       = 254
	maxEmailLocalPart = 64
)

// Правило имени общее для регистрации и профиля и совпадает с фронтом:
// латиница и кириллица, слова разделены одним пробелом или дефисом.
var namePattern = regexp.MustCompile(`^[A-Za-zА-Яа-яЁё]+(?:[ -][A-Za-zА-Яа-яЁё]+)*$`)

// Правило email для новых адресов, совпадает с фронтом. Длины проверяются
// отдельно: RE2 не поддерживает lookahead.
var emailPattern = regexp.MustCompile(
	`^[A-Za-z0-9_%+'-]+(?:\.[A-Za-z0-9_%+'-]+)*@(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$`,
)

func ValidatePassword(password string) error {
	passwordLen := len(password)

	if passwordLen < 8 || passwordLen > 72 {
		return apperr.ErrBadRequest.WithMessage("password must be 8-72 bytes long")
	}
	if strings.TrimSpace(password) == "" {
		return apperr.ErrBadRequest.WithMessage("password must not consist only of spaces")
	}

	return nil
}

// ValidateEmail проверяет адрес, который уже может быть в базе (вход,
// повтор письма, сброс пароля). Строгое правило здесь отрезало бы
// аккаунты, заведённые до его появления.
func ValidateEmail(email string) error {
	email = strings.TrimSpace(email)

	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return apperr.ErrBadRequest.WithMessage("invalid email")
	}

	return nil
}

// ValidateNewEmail проверяет адрес, который сохраняется впервые:
// регистрация и смена email.
func ValidateNewEmail(email string) error {
	email = strings.TrimSpace(email)

	if len(email) < minEmailLen || len(email) > maxEmailLen {
		return apperr.ErrBadRequest.WithMessage("email must be 6-254 characters long")
	}
	if at := strings.LastIndexByte(email, '@'); at > maxEmailLocalPart {
		return apperr.ErrBadRequest.WithMessage("email local part must not exceed 64 characters")
	}
	if !emailPattern.MatchString(email) {
		return apperr.ErrBadRequest.WithMessage("invalid email")
	}

	return nil
}

// ValidateName проверяет имя пользователя; field попадает в текст ошибки,
// чтобы клиент видел, какое поле не прошло.
func ValidateName(field, userName string) error {
	userName = strings.TrimSpace(userName)
	lenName := utf8.RuneCountInString(userName)

	if lenName < minNameRunes || lenName > maxNameRunes {
		return apperr.ErrBadRequest.WithMessage(field + " must be 2-100 characters long")
	}
	if !namePattern.MatchString(userName) {
		return apperr.ErrBadRequest.WithMessage(
			field + " must contain only Latin or Cyrillic letters, single spaces or hyphens between words",
		)
	}

	return nil
}
