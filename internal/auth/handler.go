// Package auth contains authentication handlers and services.
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

//go:generate mockgen -source=handler.go -destination=mock_service_test.go -package=auth
type authServiceIface interface {
	verifyEmail(ctx context.Context, verifyToken string) error
	resendEmail(ctx context.Context) error
}

type authHandlers struct {
	svc authServiceIface
}

func NewAuthHandler(svc authServiceIface) *authHandlers {
	return &authHandlers{svc: svc}
}

const verifyTokenLength = 43

type verifyEmailRequest struct {
	Token string `json:"token"`
}

func (h *authHandlers) VerifyEmail(w http.ResponseWriter, r *http.Request) error {
	var req verifyEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest
	}

	if len(req.Token) != verifyTokenLength {
		return apperr.ErrBadRequest
	}

	if err := h.svc.verifyEmail(r.Context(), req.Token); err != nil {
		return err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *authHandlers) ResendEmail(w http.ResponseWriter, r *http.Request) error {
	err := a.svc.resendEmail(r.Context())
	if err != nil {
		return err
	}

	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (h *authHandlers) Login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write([]byte(`{"error":"Not implemented"}`)); err != nil {
		return
	}
}

func (h *authHandlers) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write([]byte(`{"error":"Not implemented"}`)); err != nil {
		return
	}
}

func (h *authHandlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write([]byte(`{"error":"Not implemented"}`)); err != nil {
		return
	}
}

func (h *authHandlers) VerifyResend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write([]byte(`{"error":"Not implemented"}`)); err != nil {
		return
	}
}

func (h *authHandlers) EmailConfirm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if _, err := w.Write([]byte(`{"error":"Not implemented"}`)); err != nil {
		return
	}
}

func (h *authHandlers) RegisterRoutes(
	mux *http.ServeMux,
	authMW *middleware.AuthMW,
	cache *cache.Client,
	cfg *config.Config,
) {
	verifyEmailIPLimit := middleware.RateLimit(
		cache, "email-confirm",
		int64(cfg.Auth.EmailConfirmRateLimit),
		1*time.Minute, cfg.App.TrustedProxies,
	)
	verifyResendIPLimit := middleware.RateLimit(
		cache, "verify-resend",
		int64(cfg.Auth.VerifyResendRateLimit),
		1*time.Minute, cfg.App.TrustedProxies,
	)
	resendPolicy := middleware.RateLimitPolicy{
		Scope:  cfg.RateLimit.Resend.Scope,
		Limit:  cfg.RateLimit.Resend.Limit,
		Window: cfg.RateLimit.Resend.Window,
	}

	mux.Handle("POST /auth/verify-email",
		verifyEmailIPLimit(
			middleware.ErrorMiddleware(h.VerifyEmail),
		),
	)

	mux.Handle("POST /auth/verify-email/resend",
		verifyResendIPLimit(
			middleware.ErrorMiddleware(
				authMW.AuthMiddleware(
					middleware.RateLimitByUser(cache, resendPolicy)(h.ResendEmail),
				),
			),
		),
	)
}
