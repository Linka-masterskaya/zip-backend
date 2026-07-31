package httpapi

import (
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

func TestNewRateLimitsPopulatesEveryLimiter(t *testing.T) {
	cfg := &config.Config{}
	cfg.Auth.PackRateLimit = 60
	cfg.Auth.LoginRateLimit = 5
	cfg.Auth.RefreshRateLimit = 30
	cfg.Auth.ForgotRateLimit = 3
	cfg.Auth.ResetRateLimit = 3
	cfg.Auth.EmailConfirmRateLimit = 10
	cfg.Auth.VerifyResendRateLimit = 3
	cfg.Profile.EmailChangeRateLimit = 3
	cfg.Profile.EmailConfirmRateLimit = 10
	cfg.PicturesBank.InboundPerMinute = 120

	rl := NewRateLimits(nil, cfg)

	limiters := map[string]Middleware{
		"Packs":               rl.Packs,
		"Pictures":            rl.Pictures,
		"Login":               rl.Login,
		"Refresh":             rl.Refresh,
		"Forgot":              rl.Forgot,
		"Reset":               rl.Reset,
		"VerifyEmail":         rl.VerifyEmail,
		"VerifyResend":        rl.VerifyResend,
		"ProfileEmailChange":  rl.ProfileEmailChange,
		"ProfileEmailConfirm": rl.ProfileEmailConfirm,
	}
	for name, limiter := range limiters {
		if limiter == nil {
			t.Errorf("RateLimits.%s is nil", name)
		}
	}
}
