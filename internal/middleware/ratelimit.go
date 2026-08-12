package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
)

const maxBodySize = 1024 * 128

type EndpointPolicy struct {
	IPLimit        int64
	IPWindow       time.Duration
	IdentityLimit  int64
	IdentityWindow time.Duration
	GlobalLimit    int64
	GlobalWindow   time.Duration
}

// RateLimit creates an HTTP middleware for fixed-window rate limiting with IP and identity checks.
func RateLimit(cacheClient *cache.Client, scope string, policy EndpointPolicy, trustedProxies []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			email, token := extractPayloadSafe(r)
			ip := extractIP(r, trustedProxies)

			allowed, retry, err := enforcePolicies(r.Context(), cacheClient, scope, policy, ip, email, token)
			if err != nil {
				slog.Error("rate limit check failed, failing closed", slog.String("scope", scope), slog.Any("error", err))
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			if !allowed {
				failLimit(w, retry)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func enforcePolicies(ctx context.Context, c *cache.Client, scope string, policy EndpointPolicy, ip, email, token string) (bool, int64, error) {
	type policyCheck struct {
		subScope string
		key      string
		limit    int64
		window   time.Duration
	}

	checks := make([]policyCheck, 0, 4)
	checks = append(checks, policyCheck{"ip", ip, policy.IPLimit, policy.IPWindow})

	if email != "" {
		checks = append(checks, policyCheck{"email", normalizeEmailKey(email), policy.IdentityLimit, policy.IdentityWindow})
	}
	if token != "" {
		checks = append(checks, policyCheck{"token", hashTokenKey(token), policy.IdentityLimit, policy.IdentityWindow})
	}

	checks = append(checks, policyCheck{"global", "all", policy.GlobalLimit, policy.GlobalWindow})

	for _, ch := range checks {
		if ch.limit <= 0 {
			continue
		}

		req := cache.RateLimitRequest{
			Scope:      scope + ":" + ch.subScope,
			Key:        ch.key,
			Limit:      ch.limit,
			WindowSize: ch.window,
		}

		ok, retry, err := c.Allow(ctx, req)
		if err != nil || !ok {
			return ok, retry, err
		}
	}

	return true, 0, nil
}

func failLimit(w http.ResponseWriter, retry int64) {
	w.Header().Set("Retry-After", strconv.FormatInt(retry, 10))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	if _, err := w.Write([]byte("Too Many Requests. Please try again later.")); err != nil {
		slog.Error("failed to write rate limit response", slog.Any("error", err))
	}
}

func extractPayloadSafe(r *http.Request) (email, token string) {
	if r.Method == http.MethodGet {
		return "", r.URL.Query().Get("token")
	}
	if r.Body == nil || r.Body == http.NoBody {
		return "", ""
	}

	peekBuf := make([]byte, maxBodySize)
	n, err := io.ReadFull(r.Body, peekBuf)
	peekBuf = peekBuf[:n]

	switch err {
	case io.EOF, io.ErrUnexpectedEOF:
		r.Body = io.NopCloser(bytes.NewBuffer(peekBuf))
	case nil:
		r.Body = struct {
			io.Reader
			io.Closer
		}{
			Reader: io.MultiReader(bytes.NewReader(peekBuf), r.Body),
			Closer: r.Body,
		}
	default:
		return "", ""
	}

	var doc struct {
		Email string `json:"email"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(peekBuf, &doc); err != nil {
		slog.Debug("failed to parse rate limit payload, ignoring", slog.Any("error", err))
	}

	token = doc.Token
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	return doc.Email, token
}

func normalizeEmailKey(email string) string {
	email = strings.TrimSpace(strings.ToLower(email))
	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 {
		local := parts[0]
		if idx := strings.Index(local, "+"); idx != -1 {
			local = local[:idx]
		}
		return local + "@" + parts[1]
	}
	return email
}

func hashTokenKey(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
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
		return strings.TrimSpace(ips[len(ips)-1])
	}

	return remoteIP
}

type RateLimitPolicy struct {
	Scope  string        `mapstructure:"scope"`
	Limit  int64         `mapstructure:"limit"`
	Window time.Duration `mapstructure:"window"`
}

func RateLimitByUser(c *cache.Client, p RateLimitPolicy) func(AppHandler) AppHandler {
	return func(next AppHandler) AppHandler {
		return func(w http.ResponseWriter, r *http.Request) error {
			userID, err := authctx.UserIDFromCtx(r.Context())
			if err != nil {
				return apperr.ErrInternal.WithError(fmt.Errorf("rate limit: no userID in ctx (auth middleware not in chain): %w", err))
			}

			rlCtx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
			defer cancel()

			allowed, retry, err := c.Allow(rlCtx, cache.RateLimitRequest{
				Scope: p.Scope, Key: userID.String(),
				Limit: p.Limit, WindowSize: p.Window,
			})
			if err != nil {
				return apperr.ErrInternal.WithError(fmt.Errorf("cache.Allow: %w", err))
			}
			if !allowed {
				w.Header().Set("Retry-After", strconv.FormatInt(retry, 10))
				return apperr.ErrTooManyRequests
			}
			return next(w, r)
		}
	}
}
