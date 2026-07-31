package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

//go:generate mockgen -source=handler.go -destination=mock_service_test.go -package=auth
type authServiceIface interface {
	Login(ctx context.Context, email, password string) (*LoginResult, error)
	Refresh(ctx context.Context, refreshToken string) (*LoginResult, error)
	Logout(ctx context.Context, refreshToken string) error
	ForgotPassword(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, token string, newPassword string) error
	verifyEmail(ctx context.Context, verifyToken string) error
	resendEmail(ctx context.Context, email string) error
}

// Handler serves authentication HTTP endpoints.
type Handler struct {
	svc             authServiceIface
	refreshTokenTTL time.Duration
	cookieSecure    bool
}

// NewHandler creates an auth HTTP handler.
func NewHandler(svc authServiceIface, cfg ...Config) *Handler {
	h := &Handler{
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

// ForgotPasswordRequest описывает тело запроса на восстановление пароля.
type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

// ResendEmailRequest описывает тело запроса на повторную отправку письма верификации.
type ResendEmailRequest struct {
	Email string `json:"email"`
}

// ResetPasswordRequest описывает тело запроса на установку нового пароля по токену.
type ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) error {
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

	resp := LoginResponse{
		AccessToken: result.AccessToken,
	}

	//nolint:gosec // The access token is intentionally serialized into the API response.
	body, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal login response: %w", err)
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

	//nolint:gosec // The access token is intentionally returned in the response.
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("write login response: %w", err)
	}

	return nil
}

const verifyTokenLength = 43

type verifyEmailRequest struct {
	Token string `json:"token"`
}

func (h *Handler) VerifyEmail(w http.ResponseWriter, r *http.Request) error {
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

func (h *Handler) ResendEmail(w http.ResponseWriter, r *http.Request) error {
	var req ResendEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithMessage("invalid JSON request body")
	}

	if err := h.svc.resendEmail(r.Context(), req.Email); err != nil {
		return err
	}

	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) error {
	var req ForgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithMessage("invalid JSON request body")
	}

	if err := h.svc.ForgotPassword(r.Context(), req.Email); err != nil {
		return err
	}

	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) error {
	var req ResetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithMessage("invalid JSON request body")
	}

	if err := h.svc.ResetPassword(r.Context(), req.Token, req.NewPassword); err != nil {
		return err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie("refresh_token")
	if errors.Is(err, http.ErrNoCookie) {
		return apperr.ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("get refresh cookie: %w", err)
	}
	if cookie.Value == "" {
		return apperr.ErrUnauthorized
	}
	result, err := h.svc.Refresh(r.Context(), cookie.Value)
	if err != nil {
		return err
	}

	resp := LoginResponse{
		AccessToken: result.AccessToken,
	}

	//nolint:gosec // The access token is intentionally serialized into the API response.
	body, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal refresh response: %w", err)
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

	//nolint:gosec // Returning the access token is the expected API behavior.
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("write refresh response: %w", err)
	}

	return nil
}

// Logout revokes the refresh token family and clears the cookie.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie("refresh_token"); err == nil {
		if err := h.svc.Logout(r.Context(), cookie.Value); err != nil {
			return err
		}
	}

	//nolint:gosec // Secure is configured separately for local and production environments.
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusNoContent)
	return nil
}
