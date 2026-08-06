package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
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

type yandexUserInfo struct {
	ID        string `json:"id"`
	Email     string `json:"default_email"`
	Name      string `json:"display_name"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}
type OAuthHandler struct {
	service     *authService
	cache       *cache.Client
	oauthCfg    *oauth2.Config
	frontendURL string
}

func NewOAuthHandler(service *authService, cache *cache.Client, oauthCfg *oauth2.Config, frontendURL string) *OAuthHandler {
	return &OAuthHandler{
		service:     service,
		cache:       cache,
		oauthCfg:    oauthCfg,
		frontendURL: frontendURL,
	}
}

func (h *OAuthHandler) YandexLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		http.Error(w, "Failed to generate state", http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(stateBytes)
	if err := h.cache.SaveOAuthState(ctx, state, 5*time.Minute); err != nil {
		http.Error(w, "Failed to save state", http.StatusInternalServerError)
		return
	}
	url := h.oauthCfg.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (h *OAuthHandler) YandexCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		http.Error(w, "Missing code or state", http.StatusBadRequest)
		return
	}

	if err := h.validateState(ctx, state); err != nil {
		http.Error(w, "Invalid or expired state", http.StatusForbidden)
		return
	}

	token, err := h.exchangeCode(ctx, code)
	if err != nil {
		slog.Error("failed to exchange token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	yandexUser, err := h.fetchUserInfo(ctx, token)
	if err != nil {
		slog.Error("failed to fetch user info", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	name := h.buildDisplayName(yandexUser)
	user, userAuth, err := h.service.UpsertUser(ctx, yandexUser.Email, name, yandexUser.ID)
	if err != nil {
		slog.Error("failed to upsert user", "error", err)
		if errors.Is(err, ErrEmailAlreadyRegistered) {
			http.Redirect(w, r, h.frontendURL+"/login?email_exists=true", http.StatusSeeOther)
			return
		}
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if !user.EmailVerified {
		userID, err := uuid.Parse(user.ID)
		if err != nil {
			slog.Error("invalid user ID format", "user_id", user.ID, "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		if err := h.service.SendVerificationEmail(ctx, userID); err != nil {
			slog.Error("failed to send verification email", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, h.frontendURL+"/verify-email", http.StatusSeeOther)
		return
	}

	tokenString, err := h.service.GenerateOAuthJWT(user, userAuth)
	if err != nil {
		slog.Error("failed to generate JWT", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    tokenString,
		HttpOnly: true,
		Secure:   true,
		Path:     "/",
		MaxAge:   86400,
	})
	http.Redirect(w, r, h.frontendURL, http.StatusSeeOther)
}

func (h *OAuthHandler) validateState(ctx context.Context, state string) error {
	savedState, err := h.cache.GetOAuthState(ctx, state)
	if err != nil {
		return fmt.Errorf("invalid or expired state")
	}
	if savedState != state {
		return fmt.Errorf("state mismatch")
	}
	if err := h.cache.DeleteOAuthState(ctx, state); err != nil {
		slog.Warn("failed to delete oauth state", "error", err)
	}
	return nil
}

func (h *OAuthHandler) exchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	return h.oauthCfg.Exchange(ctx, code)
}

func (h *OAuthHandler) fetchUserInfo(ctx context.Context, token *oauth2.Token) (*yandexUserInfo, error) {
	client := h.oauthCfg.Client(ctx, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://login.yandex.ru/info?format=json", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Error("failed to close yandex api response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yandex API error: %s", resp.Status)
	}

	var yandexUser yandexUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&yandexUser); err != nil {
		return nil, fmt.Errorf("failed to parse user info: %w", err)
	}
	return &yandexUser, nil
}

func (h *OAuthHandler) buildDisplayName(user *yandexUserInfo) string {
	if user.Name != "" {
		return user.Name
	}
	if user.FirstName != "" || user.LastName != "" {
		name := strings.TrimSpace(user.FirstName + " " + user.LastName)
		if name != "" {
			return name
		}
	}
	return user.Email
}
