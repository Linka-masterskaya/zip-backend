package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/profile"
)

// ProfileHandlers contains the handlers exposed by the profile API.
type ProfileHandlers struct {
	Profile        *profile.Handler
	ChangePassword *profile.ChangePasswordHandler
}

// RegisterProfileRoutes registers profile, avatar, email change and password change routes.
func RegisterProfileRoutes(mux Mux, authMW *middleware.AuthMW, rl RateLimits, h ProfileHandlers) {
	protected := func(next middleware.AppHandler) http.Handler {
		return middleware.ErrorMiddleware(authMW.AuthMiddleware(next))
	}
	limited := func(limit Middleware, next middleware.AppHandler) http.Handler {
		return limit(middleware.ErrorMiddleware(authMW.AuthMiddleware(next)))
	}

	mux.Handle("GET /api/v1/profile/me", protected(h.Profile.GetProfile))
	mux.Handle("PATCH /api/v1/profile/me", protected(h.Profile.UpdateProfile))
	mux.Handle("DELETE /api/v1/profile/me", protected(h.Profile.DeleteProfile))
	mux.Handle("PUT /api/v1/profile/me/avatar", protected(h.Profile.UploadAvatar))
	mux.Handle("DELETE /api/v1/profile/me/avatar", protected(h.Profile.DeleteAvatar))
	mux.Handle("POST /api/v1/profile/me/email", limited(rl.ProfileEmailChange, h.Profile.RequestEmailChange))
	mux.Handle("POST /api/v1/profile/me/password", protected(h.ChangePassword.ChangePassword))

	// Confirmation arrives from an email link, so it is rate limited but not authenticated.
	mux.Handle(
		"POST /api/v1/profile/me/email/confirm",
		rl.ProfileEmailConfirm(middleware.ErrorMiddleware(h.Profile.ConfirmEmailChange)),
	)
}
