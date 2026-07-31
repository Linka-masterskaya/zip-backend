package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

// MediaHandlers contains the handlers exposed by the media API.
type MediaHandlers struct {
	Media *media.Handler
}

// RegisterMediaRoutes registers media upload, fetch, and delete routes.
func RegisterMediaRoutes(
	mux Mux,
	authMW *middleware.AuthMW,
	rateLimit Middleware,
	handlers MediaHandlers,
) {
	protected := func(next middleware.AppHandler) http.Handler {
		return rateLimit(middleware.ErrorMiddleware(authMW.AuthMiddleware(next)))
	}

	mux.Handle("POST /api/v1/media", protected(handlers.Media.Upload))
	mux.Handle("GET /api/v1/media/{id}", protected(handlers.Media.Get))
	mux.Handle("DELETE /api/v1/media/{id}", protected(handlers.Media.Delete))
}
