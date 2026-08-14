package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/auth"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

func RegisterAuthRoutes(
	mux *http.ServeMux,
	authMW *middleware.AuthMW,
	rl RateLimits,
	redis *cache.Client,
	cfg *config.Config,
	authHandler auth.AuthHandlerInterface,
) {
	authHandler.RegisterRoutes(
		mux,
		authMW,
		redis,
		cfg,
		rl.Login,
		rl.Register,
		rl.Refresh,
		rl.Forgot,
		rl.Reset,
		rl.VerifyResend,
		rl.VerifyEmail,
	)
}
