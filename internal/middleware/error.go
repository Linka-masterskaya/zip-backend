// Package middleware предоставляет посредников для обработки HTTP-запросов.
package middleware

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
)

// AppHandler описывает сигнатуру стандартной функции-хендлера, возвращающей ошибку.
type AppHandler func(w http.ResponseWriter, r *http.Request) error

// RecoveryMiddleware перехватывает panic и возвращает внутреннюю ошибку сервера.
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				reqID := authctx.RequestIDFromCtx(r.Context())

				slog.Error("panic recovered",
					"panic", rec,
					"request_id", reqID,
				)

				sendJSONError(w, apperr.ErrInternal, reqID)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// ErrorMiddleware обрабатывает возвращаемые хендлером ошибки и преобразует их в JSON.
func ErrorMiddleware(next AppHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := next(w, r); err != nil {
			reqID := authctx.RequestIDFromCtx(r.Context())
			var appErr *apperr.AppError

			if !errors.As(err, &appErr) {
				appErr = apperr.ErrInternal.WithError(err)
			}

			if appErr.HTTPStatus >= 500 {
				slog.Error("server error occurred",
					"code", appErr.Code,
					"err", appErr.Error(),
					"request_id", reqID,
				)
			} else {
				slog.Warn("client error occurred",
					"code", appErr.Code,
					"message", appErr.Message,
					"request_id", reqID,
				)
			}
			sendJSONError(w, appErr, reqID)
		}
	})
}

func sendJSONError(w http.ResponseWriter, appErr *apperr.AppError, reqID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(appErr.HTTPStatus)

	resp := JSONErrorResponse{
		Error: ErrorPayload{
			Code:      appErr.Code,
			Message:   appErr.Message,
			RequestID: reqID,
		},
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode json error response", "err", err)
	}
}

// ErrorPayload описывает структуру полезной нагрузки ошибки внутри JSON-ответа.
type ErrorPayload struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// JSONErrorResponse описывает стандартный формат ответа API при возникновении ошибки.
type JSONErrorResponse struct {
	Error ErrorPayload `json:"error"`
}
