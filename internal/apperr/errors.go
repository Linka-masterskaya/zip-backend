// Package apperr содержит стандартные типы ошибок приложения.
package apperr

import (
	"fmt"
	"net/http"
)

// AppError описывает кастомную ошибку приложения с HTTP-статусом.
type AppError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
	Err        error  `json:"-"`
}

// Стандартные ошибки приложения для использования в хендлерах.
var (
	ErrNotFound        = &AppError{Code: "NOT_FOUND", HTTPStatus: http.StatusNotFound, Message: "resource not found"}
	ErrUnauthorized    = &AppError{Code: "UNAUTHORIZED", HTTPStatus: http.StatusUnauthorized, Message: "unauthorized"}
	ErrForbidden       = &AppError{Code: "FORBIDDEN", HTTPStatus: http.StatusForbidden, Message: "forbidden"}
	ErrBadRequest      = &AppError{Code: "BAD_REQUEST", HTTPStatus: http.StatusBadRequest, Message: "bad request"}
	ErrConflict        = &AppError{Code: "CONFLICT", HTTPStatus: http.StatusConflict, Message: "conflict"}
	ErrInternal        = &AppError{Code: "INTERNAL", HTTPStatus: http.StatusInternalServerError, Message: "internal server error"}
	ErrPayloadTooLarge = &AppError{Code: "PAYLOAD_TOO_LARGE", HTTPStatus: http.StatusRequestEntityTooLarge, Message: "payload too large"}

	ErrUserNotFound       = &AppError{Code: "USER_NOT_FOUND", HTTPStatus: http.StatusNotFound, Message: "user not found"}
	ErrVerifyTokenInvalid = &AppError{Code: "VERIFY_TOKEN_INVALID", HTTPStatus: http.StatusBadRequest, Message: "verification link is invalid or expired"}
	ErrJWTTokenInvalid    = &AppError{Code: "JWT_TOKEN_INVALID", HTTPStatus: http.StatusUnauthorized, Message: "invalid or expired token"}
	ErrTooManyRequests    = &AppError{Code: "TOO_MANY_REQUESTS", HTTPStatus: http.StatusTooManyRequests, Message: "too many requests, please try again later"}
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
