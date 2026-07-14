package auth

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidEmail       = errors.New("an incorrect email address was entered")
	ErrWeakPassword       = errors.New("the password must be 8-72 bytes long")
	ErrEmailAlreadyExists = errors.New("email already exists")
)

// IsViolationUniqueness проверяет, что ошибка пришла из Postgres
// именно из-за нарушения уникальности email_hash
// нужен, чтобы ошибку базы превратить в понятную бизнес-ошибку ErrEmailAlreadyExists
func IsViolationUniqueness(err error) bool {
	var pgError *pgconn.PgError

	if errors.As(err, &pgError) {
		return pgError.Code == "23505" && pgError.ConstraintName == "auth_cred_email_hash_uniq"
	}
	return false
}
