package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/settings"
)

// SettingsHandlers contains user-scoped site settings and color-template handlers.
type SettingsHandlers struct {
	Settings *settings.Handler
}

// RegisterSettingsRoutes registers authenticated, user-scoped settings routes.
func RegisterSettingsRoutes(mux Mux, authMW *middleware.AuthMW, handlers SettingsHandlers) {
	protected := func(next middleware.AppHandler) http.Handler {
		return middleware.ErrorMiddleware(authMW.AuthMiddleware(next))
	}

	mux.Handle("GET /api/v1/settings", protected(handlers.Settings.Get))
	mux.Handle("PUT /api/v1/settings", protected(handlers.Settings.Put))
	mux.Handle("GET /api/v1/settings/templates", protected(handlers.Settings.ListTemplates))
	mux.Handle("POST /api/v1/settings/templates", protected(handlers.Settings.CreateTemplate))
	mux.Handle("DELETE /api/v1/settings/templates/{id}", protected(handlers.Settings.DeleteTemplate))
}
