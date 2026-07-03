package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
)

func RateLimit(cacheClient *cache.Client, scope string, limit int64, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := extractIP(r)

			req := cache.RateLimitRequest{
				Scope:      scope,
				Key:        ip,
				Limit:      limit,
				WindowSize: window,
			}

			allowed, retryAfter, err := cacheClient.Allow(r.Context(), req)
			if err != nil {
				slog.Error("rate limit check failed, failing closed for security",
					slog.String("scope", scope),
					slog.String("ip", ip),
					slog.Any("error", err),
				)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			if !allowed {
				w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				if _, err := w.Write([]byte("Too Many Requests. Please try again later.")); err != nil {
					return
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Извлечение реального IP адреса клиента с учетом проксирования.
func extractIP(r *http.Request) string {
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		return strings.TrimSpace(ips[0])
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
