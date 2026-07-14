package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

//go:generate mockgen -source=handler.go -destination=mock_service_test.go -package=auth
type authServiceIface interface {
	Login(ctx context.Context, email, password string) (*LoginResult, error)
	verifyEmail(ctx context.Context, verifyToken string) error
	resendEmail(ctx context.Context) error
}

type authHandlers struct {
	svc             authServiceIface
	refreshTokenTTL time.Duration
	cookieSecure    bool
}

func NewAuthHandler(svc authServiceIface, cfg ...Config) *authHandlers {
	h := &authHandlers{
		svc: svc,
	}

	if len(cfg) > 0 {
		h.refreshTokenTTL = cfg[0].RefreshTokenTTL
		h.cookieSecure = cfg[0].CookieSecure
	}

	return h
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	AccessToken string `json:"access_token"`
}

func (h *authHandlers) Login(w http.ResponseWriter, r *http.Request) error {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithError(err)
	}

	result, err := h.svc.Login(r.Context(), req.Email, req.Password)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		return apperr.ErrUnauthorized
	case errors.Is(err, ErrEmailNotVerified):
		return apperr.ErrForbidden.WithMessage("email not verified")
	case err != nil:
		return err
	}

	//nolint:gosec // Secure is configured separately for local and production environments.
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    result.RefreshToken,
		Path:     "/",
		MaxAge:   int(h.refreshTokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	w.Header().Set("Content-Type", "application/json")

	resp := LoginResponse{
		AccessToken: result.AccessToken,
	}

	//nolint:gosec // The access token is intentionally returned in the response.
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return fmt.Errorf("encode login response: %w", err)
	}

	return nil
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

func (h *authHandlers) ResendEmail(w http.ResponseWriter, r *http.Request) error {
	if err := h.svc.resendEmail(r.Context()); err != nil {
		return err
	}

	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (h *authHandlers) ForgotPassword(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)

	_, err := w.Write([]byte(`{"error":"Not implemented"}`))
	return err
}

func (h *authHandlers) ResetPassword(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)

	_, err := w.Write([]byte(`{"error":"Not implemented"}`))
	return err
}

func (h *authHandlers) VerifyResend(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)

	_, err := w.Write([]byte(`{"error":"Not implemented"}`))
	return err
}

func (h *authHandlers) EmailConfirm(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)

	_, err := w.Write([]byte(`{"error":"Not implemented"}`))
	return err
}

func (h *authHandlers) RegisterRoutes(
	mux *http.ServeMux,
	authMW *middleware.AuthMW,
	cacheClient *cache.Client,
	cfg *config.Config,
) {
	verifyEmailIPLimit := middleware.RateLimit(
		cacheClient,
		"email-confirm",
		int64(cfg.Auth.EmailConfirmRateLimit),
		time.Minute,
		cfg.App.TrustedProxies,
	)

	verifyResendIPLimit := middleware.RateLimit(
		cacheClient,
		"verify-resend",
		int64(cfg.Auth.VerifyResendRateLimit),
		time.Minute,
		cfg.App.TrustedProxies,
	)

	resendPolicy := middleware.RateLimitPolicy{
		Scope:  cfg.RateLimit.Resend.Scope,
		Limit:  cfg.RateLimit.Resend.Limit,
		Window: cfg.RateLimit.Resend.Window,
	}

	mux.Handle(
		"POST /api/v1/auth/email-confirm",
		verifyEmailIPLimit(
			middleware.ErrorMiddleware(h.VerifyEmail),
		),
	)

	mux.Handle(
		"POST /api/v1/auth/verify-resend",
		verifyResendIPLimit(
			middleware.ErrorMiddleware(
				authMW.AuthMiddleware(
					middleware.RateLimitByUser(cacheClient, resendPolicy)(h.ResendEmail),
				),
			),
		),
	)
}
