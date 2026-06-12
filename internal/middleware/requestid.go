package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

type reqCtxKey string

const requestIDKey = reqCtxKey("request_id")

// RequestID добавляет request_id к каждому запросу.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.NewString()

		ctx := context.WithValue(r.Context(), requestIDKey, requestID)

		slog.With(string(requestIDKey), requestID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
