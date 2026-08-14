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
	"net/url"
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
	Refresh(ctx context.Context, refreshToken string) (*LoginResult, error)
	Logout(ctx context.Context, refreshToken string) error
	ForgotPassword(ctx context.Context, email string) error
	Register(ctx context.Context, req RegisterRequest) error
	ResetPassword(ctx context.Context, token string, newPassword string) error
	verifyEmail(ctx context.Context, verifyToken string) error
	resendEmail(ctx context.Context, email string) error
}

type AuthHandlerInterface interface {
	RegisterRoutes(
		mux *http.ServeMux,
		authMW *middleware.AuthMW,
		cacheClient *cache.Client,
		cfg *config.Config,
		loginLimit middleware.Middleware,
		registerLimit middleware.Middleware,
		refreshLimit middleware.Middleware,
		forgotLimit middleware.Middleware,
		resetLimit middleware.Middleware,
		verifyResendLimit middleware.Middleware,
		verifyEmailLimit middleware.Middleware,
	)
}

type authHandlers struct {
	svc             authServiceIface
	refreshTokenTTL time.Duration
	cookieSecure    bool
	frontendOrigin  string
	oauthHandler    *OAuthHandler
}

func NewAuthHandler(svc authServiceIface, cfg ...Config) *authHandlers {
	h := &authHandlers{
		svc: svc,
	}

	if len(cfg) > 0 {
		h.refreshTokenTTL = cfg[0].RefreshTokenTTL
		h.cookieSecure = cfg[0].CookieSecure
		h.frontendOrigin = originOf(cfg[0].FrontendURL)
	}

	return h
}

func (h *authHandlers) SetOAuthHandler(oauth *OAuthHandler) {
	h.oauthHandler = oauth
}

type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	AccessToken string `json:"access_token"`
}

// Register handles user registration.
func (h *authHandlers) Register(w http.ResponseWriter, r *http.Request) error {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithError(err)
	}

	if err := ValidateEmail(req.Email); err != nil {
		return err
	}
	if err := ValidatePassword(req.Password); err != nil {
		return err
	}

	if err := h.svc.Register(r.Context(), req); err != nil {
		return err
	}

	w.WriteHeader(http.StatusCreated)
	return nil
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
		Path:     "/api/v1/auth",
		MaxAge:   int(h.refreshTokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "application/json")
	// The access token is intentionally returned in the API response.
	//nolint:gosec
	resp := LoginResponse{
		AccessToken: result.AccessToken,
	}
	//nolint:gosec // The access token is intentionally returned in the API response.
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return fmt.Errorf("encode login response: %w", err)
	}

	return nil
}

// Refresh handles refresh token rotation.
func (h *authHandlers) Refresh(w http.ResponseWriter, r *http.Request) error {
	if err := h.checkOrigin(r); err != nil {
		return err
	}

	cookie, err := r.Cookie("refresh_token")
	if errors.Is(err, http.ErrNoCookie) {
		return apperr.ErrUnauthorized
	}
	if err != nil {
		return apperr.ErrBadRequest.WithError(err)
	}

	result, err := h.svc.Refresh(r.Context(), cookie.Value)
	if err != nil {
		return err
	}

	//nolint:gosec // Secure is configured separately for local and production environments.
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    result.RefreshToken,
		Path:     "/api/v1/auth",
		MaxAge:   int(h.refreshTokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Content-Type", "application/json")

	resp := LoginResponse{
		AccessToken: result.AccessToken,
	}

	//nolint:gosec // The access token is intentionally returned in the API response.
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return fmt.Errorf("encode refresh response: %w", err)
	}

	return nil
}

// Logout revokes the refresh token family and clears the cookie.
func (h *authHandlers) Logout(w http.ResponseWriter, r *http.Request) error {
	if err := h.checkOrigin(r); err != nil {
		return err
	}

	if cookie, err := r.Cookie("refresh_token"); err == nil {
		if err := h.svc.Logout(r.Context(), cookie.Value); err != nil {
			return err
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/api/v1/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})

	w.WriteHeader(http.StatusNoContent)
	return nil
}

// checkOrigin rejects cross-site refresh requests by comparing the Origin
// (falling back to Referer) request header against app.frontend_url. It is
// a defense-in-depth measure against CSRF alongside the SameSite=Strict
// refresh cookie.
func (h *authHandlers) checkOrigin(r *http.Request) error {
	if h.frontendOrigin == "" {
		return nil
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		if referer := r.Header.Get("Referer"); referer != "" {
			origin = originOf(referer)
		}
	}

	if origin == "" || origin != h.frontendOrigin {
		return apperr.ErrForbidden
	}

	return nil
}

// originOf returns the URL scheme and host,
// which is necessary for comparison with the Origin request header.
func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}

	return u.Scheme + "://" + u.Host
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
	var req ResendEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithError(err)
	}

	if err := ValidateEmail(req.Email); err != nil {
		return err
	}

	if err := h.svc.resendEmail(r.Context(), req.Email); err != nil {
		return err
	}

	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (h *authHandlers) ForgotPassword(w http.ResponseWriter, r *http.Request) error {
	var req ForgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithError(err)
	}

	if err := ValidateEmail(req.Email); err != nil {
		return err
	}

	if err := h.svc.ForgotPassword(r.Context(), req.Email); err != nil {
		return err
	}

	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (h *authHandlers) ResetPassword(w http.ResponseWriter, r *http.Request) error {
	var req ResetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithError(err)
	}

	if err := ValidatePassword(req.NewPassword); err != nil {
		return err
	}

	if err := h.svc.ResetPassword(r.Context(), req.Token, req.NewPassword); err != nil {
		return err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

// RegisterRoutes регистрирует все auth эндпоинты и OAuth роуты (если настроены).
func (h *authHandlers) RegisterRoutes(
	mux *http.ServeMux,
	authMW *middleware.AuthMW,
	cacheClient *cache.Client,
	cfg *config.Config,
	loginLimit middleware.Middleware,
	registerLimit middleware.Middleware,
	refreshLimit middleware.Middleware,
	forgotLimit middleware.Middleware,
	resetLimit middleware.Middleware,
	verifyResendLimit middleware.Middleware,
	verifyEmailLimit middleware.Middleware,
) {
	mux.Handle("POST /api/v1/auth/register",
		registerLimit(middleware.ErrorMiddleware(h.Register)),
	)
	mux.Handle("POST /api/v1/auth/login",
		loginLimit(middleware.ErrorMiddleware(h.Login)),
	)
	mux.Handle("POST /api/v1/auth/refresh",
		refreshLimit(middleware.ErrorMiddleware(h.Refresh)),
	)
	mux.Handle("POST /api/v1/auth/password/forgot",
		forgotLimit(middleware.ErrorMiddleware(h.ForgotPassword)),
	)
	mux.Handle("POST /api/v1/auth/password/reset",
		resetLimit(middleware.ErrorMiddleware(h.ResetPassword)),
	)

	mux.Handle("POST /api/v1/auth/logout",
		middleware.ErrorMiddleware(authMW.AuthMiddleware(h.Logout)),
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

	mux.Handle("POST /api/v1/auth/verify-email/resend",
		verifyResendIPLimit(
			middleware.ErrorMiddleware(
				middleware.RateLimitByUser(cacheClient, resendPolicy)(h.ResendEmail),
			),
		),
	)
	verifyEmailIPLimit := middleware.RateLimit(
		cacheClient,
		"email-confirm",
		int64(cfg.Auth.EmailConfirmRateLimit),
		time.Minute,
		cfg.App.TrustedProxies,
	)
	mux.Handle("POST /api/v1/auth/verify-email",
		verifyEmailIPLimit(middleware.ErrorMiddleware(h.VerifyEmail)),
	)
	if h.oauthHandler != nil {
		h.oauthHandler.RegisterOAuthRoutes(mux)
	}
}

type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

type ResendEmailRequest struct {
	Email string `json:"email"`
}

type ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type yandexUserInfo struct {
	ID        string `json:"id"`
	Email     string `json:"default_email"`
	Name      string `json:"display_name"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type OAuthHandler struct {
	service         *authService
	cache           *cache.Client
	oauthCfg        *oauth2.Config
	frontendURL     string
	secure          bool
	refreshTokenTTL time.Duration
}

// NewOAuthHandler создает OAuthHandler отдельно.
// Возвращает nil, если oauthCfg == nil.
func NewOAuthHandler(
	service *authService,
	cache *cache.Client,
	oauthCfg *oauth2.Config,
	frontendURL string,
	secure bool,
	refreshTokenTTL time.Duration,
) *OAuthHandler {
	if oauthCfg == nil {
		return nil
	}
	return &OAuthHandler{
		service:         service,
		cache:           cache,
		oauthCfg:        oauthCfg,
		frontendURL:     frontendURL,
		secure:          secure,
		refreshTokenTTL: refreshTokenTTL,
	}
}

// RegisterOAuthRoutes регистрирует OAuth эндпоинты.
func (h *OAuthHandler) RegisterOAuthRoutes(mux *http.ServeMux) {
	if h == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/auth/yandex/login", h.YandexLogin)
	mux.HandleFunc("GET /api/v1/auth/yandex/callback", h.YandexCallback)
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
	// Устанавливаем state в cookie для защиты от CSRF
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/api/v1/auth/yandex/callback",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
	})
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

	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != state {
		http.Error(w, "Invalid state", http.StatusForbidden)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    "",
		Path:     "/api/v1/auth/yandex/callback",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
	})

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

	// Валидация email
	if yandexUser.Email == "" {
		slog.Error("yandex user email is empty")
		http.Error(w, "Email not provided by Yandex", http.StatusBadRequest)
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

	// Генерируем пару токенов (как в обычном login)
	jti := uuid.NewString()
	fid := uuid.NewString()

	rec := cache.RefreshRecord{
		FID:    fid,
		Status: "active",
		UserID: user.ID,
	}
	if err := h.cache.StoreRefresh(ctx, jti, rec, h.refreshTokenTTL); err != nil {
		slog.Error("failed to store refresh token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	refreshToken, err := h.service.generateRefreshToken(user, jti)
	if err != nil {
		slog.Error("failed to generate refresh token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	accessToken, err := h.service.GenerateOAuthJWT(user, userAuth)
	if err != nil {
		slog.Error("failed to generate JWT", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Устанавливаем refresh cookie (как в обычном login)
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Path:     "/api/v1/auth",
		MaxAge:   int(h.refreshTokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteStrictMode,
	})
	redirectURL := h.frontendURL + "#access_token=" + accessToken
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
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
