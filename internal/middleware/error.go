// Package middleware предоставляет посредников для обработки HTTP-запросов.
package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
)

// AppHandler описывает сигнатуру стандартной функции-хендлера, возвращающей ошибку.
type AppHandler func(w http.ResponseWriter, r *http.Request) error

type ctxKeyRequestID struct{}

var requestIDKey = ctxKeyRequestID{}

// GetRequestID извлекает уникальный ID запроса из контекста выполнения.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// RequestIDMiddleware проверяет заголовок X-Request-Id. Если его нет — генерирует новый UUID.
// Помещает ID в контекст запроса и добавляет его в Response Headers.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		if reqID == "" {
			reqID = uuid.New().String()
		}

		ctx := context.WithValue(r.Context(), requestIDKey, reqID)
		w.Header().Set("X-Request-Id", reqID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RecoveryMiddleware перехватывает panic и возвращает внутреннюю ошибку сервера.
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				reqID := GetRequestID(r.Context())
				stack := debug.Stack()

				slog.Error("panic recovered",
					"panic", rec,
					"request_id", reqID,
					"stack", string(stack),
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
			reqID := GetRequestID(r.Context())
			var appErr *apperr.AppError

			if !errors.As(err, &appErr) {
				appErr = apperr.ErrInternal.WithError(err)
			}

			if appErr.HTTPStatus >= 500 {
				slog.Error("server error occurred",
					"code", appErr.Code,
					"err", appErr.Error(),
					"request_id", reqID,
					"stack", string(debug.Stack()),
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
