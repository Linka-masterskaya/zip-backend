package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/auth"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

// AuthHandlers contains the handlers exposed by the auth API.
type AuthHandlers struct {
	Auth *auth.Handler
}

// RegisterAuthRoutes registers login, refresh, password reset and email verification routes.
func RegisterAuthRoutes(
	mux Mux,
	authMW *middleware.AuthMW,
	rl RateLimits,
	cacheClient *cache.Client,
	h AuthHandlers,
) {
	public := func(limit Middleware, next middleware.AppHandler) http.Handler {
		return limit(middleware.ErrorMiddleware(next))
	}

	mux.Handle("POST /api/v1/auth/login", public(rl.Login, h.Auth.Login))
	mux.Handle("POST /api/v1/auth/refresh", public(rl.Refresh, h.Auth.Refresh))

	// Logout only reads the refresh cookie, so it needs no access token.
	mux.Handle("POST /api/v1/auth/logout", middleware.ErrorMiddleware(h.Auth.Logout))
	mux.Handle("POST /api/v1/auth/password/forgot", public(rl.Forgot, h.Auth.ForgotPassword))
	mux.Handle("POST /api/v1/auth/password/reset", public(rl.Reset, h.Auth.ResetPassword))
	mux.Handle("POST /api/v1/auth/verify-email", public(rl.VerifyEmail, h.Auth.VerifyEmail))

	// Resend is limited twice: by IP before authentication, then per user.
	mux.Handle(
		"POST /api/v1/auth/verify-email/resend",
		rl.VerifyResend(
			middleware.ErrorMiddleware(
				authMW.AuthMiddleware(
					middleware.RateLimitByUser(cacheClient, rl.ResendPolicy)(h.Auth.ResendEmail),
				),
			),
		),
	)
}
