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
	Register            Middleware
	Refresh             Middleware
	Forgot              Middleware
	Reset               Middleware
	VerifyEmail         Middleware
	VerifyResend        Middleware
	ProfileEmailChange  Middleware
	ProfileEmailConfirm Middleware
	TTS                 Middleware
}

// NewRateLimits builds all API rate limiters from configuration.
func NewRateLimits(c *cache.Client, cfg *config.Config) RateLimits {
	proxies := cfg.App.TrustedProxies

	limit := func(scope string, n int64) Middleware {
		policy := middleware.EndpointPolicy{
			// Умножаем на 5 (запас под NAT), чтобы легальные пользователи
			// не блокировали друг друга, но брутфорс оставался неэффективным.
			IPLimit:        n * 5,
			IPWindow:       1 * time.Minute,
			IdentityLimit:  n,
			IdentityWindow: 1 * time.Minute,
			GlobalLimit:    n * 100,
			GlobalWindow:   1 * time.Minute,
		}
		return middleware.RateLimit(c, scope, policy, proxies)
	}

	return RateLimits{
		Packs:               limit("packs_api", int64(cfg.Auth.PackRateLimit)),
		Pictures:            limit("pictures_api", cfg.PicturesBank.InboundPerMinute),
		Login:               limit("login", int64(cfg.Auth.LoginRateLimit)),
		Register:            limit("register", int64(cfg.Auth.RegisterRateLimit)),
		Refresh:             limit("refresh", int64(cfg.Auth.RefreshRateLimit)),
		Forgot:              limit("forgot", int64(cfg.Auth.ForgotRateLimit)),
		Reset:               limit("reset", int64(cfg.Auth.ResetRateLimit)),
		VerifyEmail:         limit("email-confirm", int64(cfg.Auth.EmailConfirmRateLimit)),
		VerifyResend:        limit("verify-resend", int64(cfg.Auth.VerifyResendRateLimit)),
		ProfileEmailChange:  limit("profile-email-change", int64(cfg.Profile.EmailChangeRateLimit)),
		ProfileEmailConfirm: limit("profile-email-confirm", int64(cfg.Profile.EmailConfirmRateLimit)),
		TTS:                 limit("tts_api", int64(cfg.TTS.RateLimit)),
	}
}
