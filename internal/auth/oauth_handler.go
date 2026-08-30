package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
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
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"golang.org/x/oauth2"
)

// oauthStateTTL: пользователю хватает минут на экран согласия Яндекса, а
// украденный state дольше не живёт.
const oauthStateTTL = 5 * time.Minute

const oauthStateCookie = "oauth_state"

// oauthStateCookiePath ограничивает cookie одним адресом — она нужна только
// callback'у и в остальные запросы не ездит.
const oauthStateCookiePath = "/api/v1/auth/yandex/callback"

// OAuthService — то, что нужно хендлеру от сервиса аутентификации.
type OAuthService interface {
	LoginWithYandex(ctx context.Context, profile YandexProfile) (*LoginResult, error)
}

// profileFetcher меняет код авторизации на профиль пользователя. Отдельной
// функцией, чтобы тесты не ходили в Яндекс.
type profileFetcher func(ctx context.Context, code string) (YandexProfile, error)

type OAuthHandler struct {
	service      OAuthService
	cfg          *oauth2.Config
	fetchProfile profileFetcher
	userInfoURL  string
	frontendURL  string
	cookieSecure bool
	refreshTTL   time.Duration
}

func NewOAuthHandler(
	service OAuthService,
	cfg *oauth2.Config,
	frontendURL string,
	cookieSecure bool,
	refreshTTL time.Duration,
) *OAuthHandler {
	handler := &OAuthHandler{
		service:      service,
		cfg:          cfg,
		frontendURL:  frontendURL,
		cookieSecure: cookieSecure,
		refreshTTL:   refreshTTL,
		userInfoURL:  yandexUserInfoURL,
	}
	handler.fetchProfile = handler.exchangeCodeForProfile
	return handler
}

// YandexLogin уводит пользователя на согласие Яндекса. state уезжает и в
// адрес, и в HttpOnly-cookie: сойтись они могут только у того браузера,
// который вход и начал. Хранить state на сервере не нужно — подделать
// cookie на нашем домене чужой сайт не может.
func (h *OAuthHandler) YandexLogin(w http.ResponseWriter, r *http.Request) error {
	state, err := newOAuthState()
	if err != nil {
		return err
	}

	//nolint:gosec // Secure is configured separately for local and production environments.
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     oauthStateCookiePath,
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, h.cfg.AuthCodeURL(state), http.StatusTemporaryRedirect)
	return nil
}

func newOAuthState() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", apperr.ErrInternal.WithError(err)
	}
	return hex.EncodeToString(raw), nil
}

// YandexCallback принимает пользователя обратно от Яндекса и выдаёт сессию.
func (h *OAuthHandler) YandexCallback(w http.ResponseWriter, r *http.Request) error {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		return apperr.ErrBadRequest.WithMessage("code and state are required")
	}
	if err := h.checkState(r, state); err != nil {
		return err
	}
	// Cookie гасится сразу после сверки: тем же state второй раз войти
	// нельзя, даже из того же браузера.
	clearStateCookie(w, h.cookieSecure)

	profile, err := h.fetchProfile(r.Context(), code)
	if err != nil {
		return apperr.ErrBadRequest.WithMessage("yandex authorization failed").WithError(err)
	}
	if profile.Email == "" {
		return apperr.ErrBadRequest.WithMessage("yandex account has no email")
	}

	result, err := h.service.LoginWithYandex(r.Context(), profile)
	if errors.Is(err, ErrEmailAlreadyRegistered) {
		// Аккаунт с такой почтой уже заведён паролем. Молча связывать его с
		// Яндексом нельзя, поэтому отправляем пользователя логиниться обычным
		// способом.
		http.Redirect(w, r, h.frontendURL+"/login?email_exists=true", http.StatusSeeOther)
		return nil
	}
	if err != nil {
		return err
	}

	setRefreshCookie(w, result.RefreshToken, h.refreshTTL, h.cookieSecure)
	// Токен уезжает во фрагменте, а не в query: фрагмент не попадает ни в
	// Referer, ни в логи прокси, ни в историю на стороне сервера.
	http.Redirect(w, r, h.frontendURL+"#access_token="+url.QueryEscape(result.AccessToken), http.StatusSeeOther)
	return nil
}

// checkState сверяет state из адреса с cookie того же браузера. Сравнение
// постоянное по времени — значение секретное, и подбирать его по скорости
// ответа не должно быть возможно.
func (h *OAuthHandler) checkState(r *http.Request, state string) error {
	cookie, err := r.Cookie(oauthStateCookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		return apperr.ErrForbidden.WithMessage("oauth state mismatch")
	}
	return nil
}

func clearStateCookie(w http.ResponseWriter, secure bool) {
	//nolint:gosec // Secure is configured separately for local and production environments.
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    "",
		Path:     oauthStateCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *OAuthHandler) exchangeCodeForProfile(
	ctx context.Context,
	code string,
) (YandexProfile, error) {
	token, err := h.cfg.Exchange(ctx, code)
	if err != nil {
		return YandexProfile{}, fmt.Errorf("exchange code: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, h.userInfoURL, nil,
	)
	if err != nil {
		return YandexProfile{}, fmt.Errorf("build user info request: %w", err)
	}

	resp, err := h.cfg.Client(ctx, token).Do(req)
	if err != nil {
		return YandexProfile{}, fmt.Errorf("fetch user info: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("close yandex user info body", logger.Err(closeErr))
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return YandexProfile{}, fmt.Errorf("yandex user info: %s", resp.Status)
	}

	var info yandexUserInfo
	if err = json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return YandexProfile{}, fmt.Errorf("decode user info: %w", err)
	}
	return YandexProfile{
		ID:    info.ID,
		Email: info.DefaultEmail,
		Name:  info.displayName(),
	}, nil
}

const yandexUserInfoURL = "https://login.yandex.ru/info?format=json"

type yandexUserInfo struct {
	ID           string `json:"id"`
	DefaultEmail string `json:"default_email"`
	DisplayName  string `json:"display_name"`
	RealName     string `json:"real_name"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
}

// displayName выбирает, как звать пользователя: у Яндекса заполнены не все
// поля, а имя в карточке показывается всегда.
func (u yandexUserInfo) displayName() string {
	for _, candidate := range []string{
		u.DisplayName,
		u.RealName,
		strings.TrimSpace(u.FirstName + " " + u.LastName),
	} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return u.DefaultEmail
}
