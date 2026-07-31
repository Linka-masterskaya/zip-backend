package httpapi

import (
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

// RateLimits holds every rate limiter used by the public API, so limits live
// in one place instead of being spread across bootstrap and handlers.
type RateLimits struct {
	Packs               Middleware
	Pictures            Middleware
	Login               Middleware
	Refresh             Middleware
	Forgot              Middleware
	Reset               Middleware
	VerifyEmail         Middleware
	VerifyResend        Middleware
	ProfileEmailChange  Middleware
	ProfileEmailConfirm Middleware

	// ResendPolicy is applied per user on top of the IP-based VerifyResend limit.
	ResendPolicy middleware.RateLimitPolicy
}

// NewRateLimits builds all API rate limiters from configuration.
func NewRateLimits(c *cache.Client, cfg *config.Config) RateLimits {
	proxies := cfg.App.TrustedProxies
	limit := func(scope string, n int64) Middleware {
		return middleware.RateLimit(c, scope, n, time.Minute, proxies)
	}

	return RateLimits{
		Packs:               limit("packs_api", int64(cfg.Auth.PackRateLimit)),
		Pictures:            limit("pictures_api", cfg.PicturesBank.InboundPerMinute),
		Login:               limit("login", int64(cfg.Auth.LoginRateLimit)),
		Refresh:             limit("refresh", int64(cfg.Auth.RefreshRateLimit)),
		Forgot:              limit("forgot", int64(cfg.Auth.ForgotRateLimit)),
		Reset:               limit("reset", int64(cfg.Auth.ResetRateLimit)),
		VerifyEmail:         limit("email-confirm", int64(cfg.Auth.EmailConfirmRateLimit)),
		VerifyResend:        limit("verify-resend", int64(cfg.Auth.VerifyResendRateLimit)),
		ProfileEmailChange:  limit("profile-email-change", int64(cfg.Profile.EmailChangeRateLimit)),
		ProfileEmailConfirm: limit("profile-email-confirm", int64(cfg.Profile.EmailConfirmRateLimit)),
		ResendPolicy: middleware.RateLimitPolicy{
			Scope:  cfg.RateLimit.Resend.Scope,
			Limit:  cfg.RateLimit.Resend.Limit,
			Window: cfg.RateLimit.Resend.Window,
		},
	}
}
