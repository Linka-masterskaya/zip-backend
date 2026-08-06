package middleware

import (
	"log/slog"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/google/uuid"
)

// RequestIDMiddleware генерирует уникальный X-Request-Id для каждого входящего HTTP-запроса.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		if reqID == "" {
			reqID = uuid.New().String()
		}

		ctx := authctx.SetRequestIDToCtx(r.Context(), reqID)
		r = r.WithContext(ctx)

		w.Header().Set("X-Request-Id", reqID)

		slog.Info("request started",
			"method", r.Method,
			"path", r.URL.Path,
			"request_id", reqID,
		)

		next.ServeHTTP(w, r)
	})
}
