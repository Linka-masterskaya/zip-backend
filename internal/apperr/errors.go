// Package apperr содержит стандартные типы ошибок приложения.
package apperr

import "fmt"

// AppError описывает кастомную ошибку приложения с HTTP-статусом.
type AppError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
	Err        error  `json:"-"`
}

// Стандартные ошибки приложения для использования в хендлерах.
var (
	ErrNotFound     = &AppError{Code: "NOT_FOUND", HTTPStatus: 404, Message: "resource not found"}
	ErrUnauthorized = &AppError{Code: "UNAUTHORIZED", HTTPStatus: 401, Message: "unauthorized"}
	ErrForbidden    = &AppError{Code: "FORBIDDEN", HTTPStatus: 403, Message: "forbidden"}
	ErrBadRequest   = &AppError{Code: "BAD_REQUEST", HTTPStatus: 400, Message: "bad request"}
	ErrConflict     = &AppError{Code: "CONFLICT", HTTPStatus: 409, Message: "conflict"}
	ErrInternal     = &AppError{Code: "INTERNAL", HTTPStatus: 500, Message: "internal server error"}
)

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	return e.Err
}

// WithMessage возвращает копию ошибки с новым сообщением.
func (e *AppError) WithMessage(msg string) *AppError {
	return &AppError{
		Code:       e.Code,
		Message:    msg,
		HTTPStatus: e.HTTPStatus,
		Err:        e.Err,
	}
}

// WithError возвращает копию ошибки с обернутой оригинальной ошибкой.
func (e *AppError) WithError(err error) *AppError {
	return &AppError{
		Code:       e.Code,
		Message:    e.Message,
		HTTPStatus: e.HTTPStatus,
		Err:        err,
	}
}
