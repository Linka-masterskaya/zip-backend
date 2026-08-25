package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

func newCORSConfig() config.CORSConfig {
	return config.CORSConfig{
		AllowOrigins:     []string{"https://example.com", "https://other.com"},
		AllowMethods:     []string{"GET", "POST", "PATCH"},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	}
}

func doCORSRequest(t *testing.T, cfg config.CORSConfig, method, origin string) *httptest.ResponseRecorder {
	t.Helper()

	called := false
	handler := CORSMiddleware(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	req, err := http.NewRequestWithContext(context.Background(), method, "/test", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if method == http.MethodOptions && called {
		t.Error("OPTIONS should not reach the handler")
	}
	if method != http.MethodOptions && !called {
		t.Error("non-OPTIONS should reach the handler")
	}

	return rec
}

func TestCORSMiddleware_AllowedOrigin(t *testing.T) {
	rec := doCORSRequest(t, newCORSConfig(), http.MethodGet, "https://example.com")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("Allow-Origin = %q, want %q", got, "https://example.com")
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want %q", got, "true")
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
}

func TestCORSMiddleware_DisallowedOrigin(t *testing.T) {
	rec := doCORSRequest(t, newCORSConfig(), http.MethodGet, "https://evil.com")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Allow-Credentials = %q, want empty", got)
	}
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	rec := doCORSRequest(t, newCORSConfig(), http.MethodOptions, "https://example.com")

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestCORSMiddleware_MethodsAndHeaders(t *testing.T) {
	rec := doCORSRequest(t, newCORSConfig(), http.MethodGet, "https://example.com")

	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PATCH" {
		t.Errorf("Allow-Methods = %q, want %q", got, "GET, POST, PATCH")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, Authorization" {
		t.Errorf("Allow-Headers = %q, want %q", got, "Content-Type, Authorization")
	}
}

func TestCORSMiddleware_CredentialsFalse(t *testing.T) {
	cfg := newCORSConfig()
	cfg.AllowCredentials = false
	rec := doCORSRequest(t, cfg, http.MethodGet, "https://example.com")

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "false" {
		t.Errorf("Allow-Credentials = %q, want %q", got, "false")
	}
}

func TestCORSMiddleware_NoOrigin(t *testing.T) {
	rec := doCORSRequest(t, newCORSConfig(), http.MethodGet, "")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty", got)
	}
}
