package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/auth"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"golang.org/x/oauth2"
)

type stubOAuthService struct{}

func (stubOAuthService) LoginWithYandex(
	context.Context, auth.YandexProfile,
) (*auth.LoginResult, error) {
	return &auth.LoginResult{AccessToken: "access", RefreshToken: "refresh"}, nil
}

func oauthTestHandler() *auth.OAuthHandler {
	return auth.NewOAuthHandler(
		stubOAuthService{},
		&oauth2.Config{
			ClientID:    "client-id",
			RedirectURL: "https://api.test/api/v1/auth/yandex/callback",
			Endpoint:    oauth2.Endpoint{AuthURL: "https://oauth.yandex.ru/authorize"},
		},
		"https://app.test",
		true,
		time.Hour,
	)
}

func authRateLimits() RateLimits {
	return RateLimits{
		Login: passthrough, Register: passthrough, Refresh: passthrough,
		Forgot: passthrough, Reset: passthrough,
		VerifyEmail: passthrough, VerifyResend: passthrough,
	}
}

// TestYandexLoginRouteIsReachable проверяет не строку в исходнике, а живой
// роутинг: запрос проходит mux и цепочку middleware и доходит до хендлера.
func TestYandexLoginRouteIsReachable(t *testing.T) {
	mux := http.NewServeMux()
	RegisterAuthRoutes(
		mux,
		middleware.NewAuthMW([]byte("test-secret")),
		authRateLimits(),
		nil,
		AuthHandlers{Auth: &auth.Handler{}, OAuth: oauthTestHandler()},
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/api/v1/auth/yandex/login", nil,
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307: %s", rec.Code, rec.Body.String())
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	if location.Host != "oauth.yandex.ru" {
		t.Fatalf("redirect host = %q, want oauth.yandex.ru", location.Host)
	}
	if location.Query().Get("state") == "" {
		t.Fatal("state отсутствует в адресе согласия")
	}

	var stateCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "oauth_state" {
			stateCookie = cookie
		}
	}
	if stateCookie == nil {
		t.Fatal("cookie oauth_state не выставлена")
	}
	if !stateCookie.HttpOnly {
		t.Fatal("cookie oauth_state должна быть HttpOnly")
	}
}

// TestYandexRoutesAbsentWithoutProvider: без настроенного провайдера роутов
// нет вовсе, а не 500 на пустом конфиге.
func TestYandexRoutesAbsentWithoutProvider(t *testing.T) {
	mux := http.NewServeMux()
	RegisterAuthRoutes(
		mux,
		middleware.NewAuthMW([]byte("test-secret")),
		authRateLimits(),
		nil,
		AuthHandlers{Auth: &auth.Handler{}},
	)

	for _, path := range []string{
		"/api/v1/auth/yandex/login",
		"/api/v1/auth/yandex/callback",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", path, rec.Code)
		}
	}
}
