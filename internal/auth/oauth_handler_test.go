package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func testOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "https://api.test/api/v1/auth/yandex/callback",
		Scopes:       []string{"login:email", "login:info"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://oauth.yandex.ru/authorize",
			TokenURL: "https://oauth.yandex.ru/token",
		},
	}
}

func cookieByName(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

// TestYandexLogin_BindsStateToBrowser: state уезжает и в адрес согласия, и в
// HttpOnly-cookie. Сойтись они могут только у того браузера, который вход и
// начал, — чужой сайт cookie на наш домен не поставит.
func TestYandexLogin_BindsStateToBrowser(t *testing.T) {
	handler := NewOAuthHandler(nil, testOAuthConfig(), "https://app.test", true, time.Hour)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/api/v1/auth/yandex/login", nil,
	)
	require.NoError(t, handler.YandexLogin(rec, req))

	require.Equal(t, http.StatusTemporaryRedirect, rec.Code)
	location, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "oauth.yandex.ru", location.Host)
	state := location.Query().Get("state")
	require.NotEmpty(t, state)

	cookie := cookieByName(t, rec, "oauth_state")
	require.NotNil(t, cookie)
	require.Equal(t, state, cookie.Value)
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure)
	require.Equal(t, "/api/v1/auth/yandex/callback", cookie.Path)
	require.Positive(t, cookie.MaxAge, "cookie обязана протухать сама")

	second := httptest.NewRecorder()
	require.NoError(t, handler.YandexLogin(second, req))
	secondLocation, err := url.Parse(second.Header().Get("Location"))
	require.NoError(t, err)
	require.NotEqual(t, state, secondLocation.Query().Get("state"), "state должен быть разным")
}

func callbackRequest(t *testing.T, query, cookieState string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet,
		"/api/v1/auth/yandex/callback"+query, nil,
	)
	if cookieState != "" {
		req.AddCookie(&http.Cookie{Name: "oauth_state", Value: cookieState})
	}
	return req
}

// failingOAuthService падает, если до сервиса вообще дошли: на плохом state
// обмена кода быть не должно.
type failingOAuthService struct{ t *testing.T }

func (f failingOAuthService) LoginWithYandex(context.Context, YandexProfile) (*LoginResult, error) {
	f.t.Fatal("сервис не должен вызываться при неверном state")
	return nil, nil
}

// TestYandexCallback_RejectsBadState: без совпадения адреса и cookie обмен
// кода не начинается вовсе.
func TestYandexCallback_RejectsBadState(t *testing.T) {
	cases := []struct {
		name        string
		query       string
		cookieState string
	}{
		{name: "нет cookie", query: "?code=c&state=s", cookieState: ""},
		{name: "cookie от другого входа", query: "?code=c&state=s", cookieState: "other"},
		{name: "нет кода", query: "?state=s", cookieState: "s"},
		{name: "нет state", query: "?code=c", cookieState: "s"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewOAuthHandler(
				failingOAuthService{t}, testOAuthConfig(), "https://app.test", true, time.Hour,
			)

			err := handler.YandexCallback(
				httptest.NewRecorder(), callbackRequest(t, tc.query, tc.cookieState),
			)

			require.Error(t, err)
			var appErr *apperr.AppError
			require.ErrorAs(t, err, &appErr)
			require.Contains(t, []int{http.StatusForbidden, http.StatusBadRequest}, appErr.HTTPStatus)
		})
	}
}

type stubOAuthService struct {
	profile YandexProfile
	result  *LoginResult
	err     error
}

func (s *stubOAuthService) LoginWithYandex(
	_ context.Context, profile YandexProfile,
) (*LoginResult, error) {
	s.profile = profile
	return s.result, s.err
}

func handlerWithProfile(
	service OAuthService, profile YandexProfile, fetchErr error,
) *OAuthHandler {
	handler := NewOAuthHandler(service, testOAuthConfig(), "https://app.test", true, time.Hour)
	handler.fetchProfile = func(context.Context, string) (YandexProfile, error) {
		return profile, fetchErr
	}
	return handler
}

// TestYandexCallback_IssuesSession: refresh уезжает в HttpOnly-cookie, access
// — во фрагменте адреса. В query его класть нельзя: он утечёт в Referer,
// историю браузера и логи прокси.
func TestYandexCallback_IssuesSession(t *testing.T) {
	service := &stubOAuthService{result: &LoginResult{AccessToken: "access-1", RefreshToken: "refresh-1"}}
	handler := handlerWithProfile(service, YandexProfile{
		ID: "yandex-1", Email: "user@example.com", Name: "Аня",
	}, nil)

	rec := httptest.NewRecorder()
	require.NoError(t, handler.YandexCallback(rec, callbackRequest(t, "?code=c&state=state-1", "state-1")))

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "https://app.test#access_token=access-1", rec.Header().Get("Location"))

	refresh := cookieByName(t, rec, "refresh_token")
	require.NotNil(t, refresh, "refresh-cookie не выставлена")
	require.Equal(t, "refresh-1", refresh.Value)
	require.True(t, refresh.HttpOnly)
	require.Equal(t, "/api/v1/auth", refresh.Path)

	require.Equal(t, "yandex-1", service.profile.ID)
}

// TestYandexCallback_StateIsOneTime: cookie гасится сразу после сверки,
// поэтому повторный заход по тому же адресу не пройдёт даже из того же
// браузера.
func TestYandexCallback_StateIsOneTime(t *testing.T) {
	service := &stubOAuthService{result: &LoginResult{AccessToken: "access-1", RefreshToken: "refresh-1"}}
	handler := handlerWithProfile(service, YandexProfile{
		ID: "yandex-1", Email: "user@example.com",
	}, nil)

	rec := httptest.NewRecorder()
	require.NoError(t, handler.YandexCallback(rec, callbackRequest(t, "?code=c&state=state-1", "state-1")))

	state := cookieByName(t, rec, "oauth_state")
	require.NotNil(t, state, "cookie состояния должна гаситься явно")
	require.Empty(t, state.Value)
	require.Negative(t, state.MaxAge, "cookie должна удаляться, а не жить дальше")
}

// TestYandexCallback_RejectsAccountWithoutEmail: без почты аккаунт завести
// нельзя — на ней держится и вход по паролю, и восстановление.
func TestYandexCallback_RejectsAccountWithoutEmail(t *testing.T) {
	handler := handlerWithProfile(failingOAuthService{t}, YandexProfile{ID: "yandex-1"}, nil)

	err := handler.YandexCallback(
		httptest.NewRecorder(), callbackRequest(t, "?code=c&state=state-1", "state-1"),
	)

	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusBadRequest, appErr.HTTPStatus)
}

// TestYandexCallback_ExistingLocalAccount: почта уже занята локальным
// аккаунтом — уводим логиниться паролем, а не связываем молча.
func TestYandexCallback_ExistingLocalAccount(t *testing.T) {
	service := &stubOAuthService{err: ErrEmailAlreadyRegistered}
	handler := handlerWithProfile(service, YandexProfile{
		ID: "yandex-1", Email: "taken@example.com",
	}, nil)

	rec := httptest.NewRecorder()
	require.NoError(t, handler.YandexCallback(rec, callbackRequest(t, "?code=c&state=state-1", "state-1")))

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "https://app.test/login?email_exists=true", rec.Header().Get("Location"))
	require.Nil(t, cookieByName(t, rec, "refresh_token"), "сессия не выдаётся")
}

// TestExchangeCodeForProfile проверяет настоящий путь к провайдеру: обмен
// кода на токен и разбор профиля. В остальных тестах он подменён целиком,
// поэтому иначе этот код не проверялся бы вовсе.
func TestExchangeCodeForProfile(t *testing.T) {
	var gotAuthHeader string
	yandex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"token-1","token_type":"bearer"}`))
		case "/info":
			gotAuthHeader = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "yandex-1",
				"default_email": "user@example.com",
				"display_name": "",
				"real_name": "",
				"first_name": "Анна",
				"last_name": "Петрова"
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer yandex.Close()

	cfg := testOAuthConfig()
	cfg.Endpoint = oauth2.Endpoint{AuthURL: yandex.URL + "/authorize", TokenURL: yandex.URL + "/token"}
	handler := NewOAuthHandler(nil, cfg, "https://app.test", true, time.Hour)
	handler.userInfoURL = yandex.URL + "/info"

	profile, err := handler.exchangeCodeForProfile(context.Background(), "code-1")
	require.NoError(t, err)
	require.Equal(t, "yandex-1", profile.ID)
	require.Equal(t, "user@example.com", profile.Email)
	require.Equal(t, "Анна Петрова", profile.Name, "имя собирается из частей, если display_name пуст")
	require.Equal(t, "Bearer token-1", gotAuthHeader, "токен должен уходить в заголовке")
}

// TestExchangeCodeForProfile_ProviderError: Яндекс ответил ошибкой — вход
// обязан упасть, а не считать профиль пустым.
func TestExchangeCodeForProfile_ProviderError(t *testing.T) {
	yandex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"token-1","token_type":"bearer"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer yandex.Close()

	cfg := testOAuthConfig()
	cfg.Endpoint = oauth2.Endpoint{AuthURL: yandex.URL + "/authorize", TokenURL: yandex.URL + "/token"}
	handler := NewOAuthHandler(nil, cfg, "https://app.test", true, time.Hour)
	handler.userInfoURL = yandex.URL + "/info"

	_, err := handler.exchangeCodeForProfile(context.Background(), "code-1")
	require.Error(t, err)
}
