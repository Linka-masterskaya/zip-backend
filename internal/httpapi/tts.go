package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/tts"
)

// TTSHandlers contains the handlers exposed by the tts API.
type TTSHandlers struct {
	TTS *tts.Handler
}

func RegisterTTSRoutes(
	mux Mux,
	authMW *middleware.AuthMW,
	rateLimit Middleware,
	handlers TTSHandlers,
) {
	protected := func(next middleware.AppHandler) http.Handler {
		return rateLimit(middleware.ErrorMiddleware(authMW.AuthMiddleware(next)))
	}

	mux.Handle("POST /api/v1/tts", protected(handlers.TTS.Create))
	mux.Handle("GET /api/v1/tts/voices", protected(handlers.TTS.Voices))
	mux.Handle("GET /api/v1/tts/{id}", protected(handlers.TTS.Get))
}
