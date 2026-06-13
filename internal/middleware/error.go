package middleware

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

type AppHandler func(w http.ResponseWriter, r *http.Request) error

type ErrorPayload struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type JSONErrorResponse struct {
	Error ErrorPayload `json:"error"`
}

func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")

		defer func() {
			if rec := recover(); rec != nil {
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

func ErrorMiddleware(next AppHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")

		if err := next(w, r); err != nil {
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

	_ = json.NewEncoder(w).Encode(resp)
}
