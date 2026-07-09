package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
)

// RateLimit creates an HTTP middleware for fixed-window rate limiting with IP and identity checks.
func RateLimit(cacheClient *cache.Client, scope string, limit int64, window time.Duration, trustedProxies []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := extractIP(r, trustedProxies)

			reqIP := cache.RateLimitRequest{
				Scope:      scope + ":ip",
				Key:        ip,
				Limit:      limit,
				WindowSize: window,
			}

			allowedIP, retryAfterIP, err := cacheClient.Allow(r.Context(), reqIP)
			if err != nil {
				slog.Error("rate limit IP check failed, failing closed for security",
					slog.String("scope", scope),
					slog.String("ip", ip),
					slog.Any("error", err),
				)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			email := extractEmail(r)
			allowedEmail := true
			var retryAfterEmail int64

			if email != "" {
				reqEmail := cache.RateLimitRequest{
					Scope:      scope + ":email",
					Key:        email,
					Limit:      limit,
					WindowSize: window,
				}

				var errEmail error
				allowedEmail, retryAfterEmail, errEmail = cacheClient.Allow(r.Context(), reqEmail)
				if errEmail != nil {
					slog.Error("rate limit email check failed, failing closed for security",
						slog.String("scope", scope),
						slog.String("email", email),
						slog.Any("error", errEmail),
					)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					return
				}
			}

			if !allowedIP || !allowedEmail {
				maxRetry := retryAfterIP
				if retryAfterEmail > maxRetry {
					maxRetry = retryAfterEmail
				}

				w.Header().Set("Retry-After", strconv.FormatInt(maxRetry, 10))
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				if _, errWrite := w.Write([]byte("Too Many Requests. Please try again later.")); errWrite != nil {
					return
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func extractIP(r *http.Request, trustedProxies []string) string {
	remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteIP = r.RemoteAddr
	}

	isTrusted := false
	for _, proxy := range trustedProxies {
		if remoteIP == proxy {
			isTrusted = true
			break
		}
		if _, cidrnet, errCIDR := net.ParseCIDR(proxy); errCIDR == nil {
			if parsedIP := net.ParseIP(remoteIP); parsedIP != nil && cidrnet.Contains(parsedIP) {
				isTrusted = true
				break
			}
		}
	}

	if !isTrusted {
		return remoteIP
	}

	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		return strings.TrimSpace(ips[0])
	}

	return remoteIP
}

func extractEmail(r *http.Request) string {
	if r.Body == nil || r.Method == http.MethodGet {
		return ""
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1024*128))
	if err != nil {
		return ""
	}
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	var doc struct {
		Email string `json:"email"`
	}
	if errUnmarshal := json.Unmarshal(bodyBytes, &doc); errUnmarshal == nil && doc.Email != "" {
		return strings.ToLower(strings.TrimSpace(doc.Email))
	}
	return ""
}
