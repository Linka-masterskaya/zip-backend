package httpapi

import (
	"net/http"
	"sort"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

type recordingMux struct {
	patterns []string
}

func (m *recordingMux) Handle(pattern string, _ http.Handler) {
	m.patterns = append(m.patterns, pattern)
}

func passthrough(next http.Handler) http.Handler { return next }

func assertPatterns(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(got)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)

	if len(got) != len(sorted) {
		t.Fatalf("registered %d routes, want %d\ngot:  %v\nwant: %v", len(got), len(sorted), got, sorted)
	}
	for i := range sorted {
		if got[i] != sorted[i] {
			t.Fatalf("route mismatch at %d: got %q, want %q", i, got[i], sorted[i])
		}
	}
}

func TestRegisterPackRoutesPatterns(t *testing.T) {
	m := &recordingMux{}
	RegisterPackRoutes(m, middleware.NewAuthMW([]byte("test-secret")), passthrough, PackHandlers{})

	assertPatterns(t, m.patterns, []string{
		"POST /api/v1/packs",
		"GET /api/v1/packs/{id}",
		"GET /api/v1/packs",
		"PATCH /api/v1/packs/{id}",
		"DELETE /api/v1/packs/{id}",
		"POST /api/v1/packs/{id}/move",
		"POST /api/v1/packs/{id}/publication",
		"DELETE /api/v1/packs/{id}/publication",
		"PUT /api/v1/packs/{id}/config",
		"GET /api/v1/packs/{id}/export",
		"POST /api/v1/packs/import",
		"POST /api/v1/packs/{id}/students",
		"DELETE /api/v1/packs/{id}/students/{student_id}",
		"POST /api/v1/packs/{id}/versions",
		"GET /api/v1/packs/{id}/versions",
		"GET /api/v1/packs/{id}/versions/{version}",
		"POST /api/v1/packs/{id}/versions/{version}/restore",
	})
}

func TestRegisterMediaRoutesPatterns(t *testing.T) {
	m := &recordingMux{}
	RegisterMediaRoutes(m, middleware.NewAuthMW([]byte("test-secret")), passthrough, MediaHandlers{})

	assertPatterns(t, m.patterns, []string{
		"POST /api/v1/media",
		"GET /api/v1/media/{id}",
		"DELETE /api/v1/media/{id}",
	})
}

func TestRegisterFolderRoutesPatterns(t *testing.T) {
	m := &recordingMux{}
	RegisterFolderRoutes(m, middleware.NewAuthMW([]byte("test-secret")), passthrough, FolderHandlers{})

	assertPatterns(t, m.patterns, []string{
		"POST /api/v1/folders",
		"GET /api/v1/folders",
		"GET /api/v1/folders/{id}/contents",
		"PATCH /api/v1/folders/{id}",
		"POST /api/v1/folders/{id}/move",
		"DELETE /api/v1/folders/{id}",
	})
}

func TestRegisterStudentRoutesPatterns(t *testing.T) {
	m := &recordingMux{}
	RegisterStudentRoutes(m, middleware.NewAuthMW([]byte("test-secret")), passthrough, StudentHandlers{})

	assertPatterns(t, m.patterns, []string{
		"POST /api/v1/students",
		"GET /api/v1/students",
		"PATCH /api/v1/students/{id}",
		"DELETE /api/v1/students/{id}",
	})
}

func TestRegisterPictureBankRoutesPatterns(t *testing.T) {
	m := &recordingMux{}
	RegisterPictureBankRoutes(m, middleware.NewAuthMW([]byte("test-secret")), passthrough, nil)

	assertPatterns(t, m.patterns, []string{
		"GET /api/v1/pictures/categories",
		"GET /api/v1/pictures/search",
		"GET /api/v1/pictures/{id}/content",
		"POST /api/v1/pictures/{id}/import",
	})
}

func TestRegisterAuthRoutesPatterns(t *testing.T) {
	m := &recordingMux{}
	rl := RateLimits{
		Login:        passthrough,
		Register:     passthrough,
		Refresh:      passthrough,
		Forgot:       passthrough,
		Reset:        passthrough,
		VerifyEmail:  passthrough,
		VerifyResend: passthrough,
	}
	RegisterAuthRoutes(m, middleware.NewAuthMW([]byte("test-secret")), rl, nil, AuthHandlers{})

	assertPatterns(t, m.patterns, []string{
		"POST /api/v1/auth/login",
		"POST /api/v1/auth/refresh",
		"POST /api/v1/auth/register",
		"POST /api/v1/auth/logout",
		"POST /api/v1/auth/password/forgot",
		"POST /api/v1/auth/password/reset",
		"POST /api/v1/auth/verify-email",
		"POST /api/v1/auth/verify-email/resend",
	})
}

func TestRegisterProfileRoutesPatterns(t *testing.T) {
	m := &recordingMux{}
	rl := RateLimits{
		ProfileEmailChange:  passthrough,
		ProfileEmailConfirm: passthrough,
	}
	RegisterProfileRoutes(m, middleware.NewAuthMW([]byte("test-secret")), rl, ProfileHandlers{})

	assertPatterns(t, m.patterns, []string{
		"GET /api/v1/profile/me",
		"PATCH /api/v1/profile/me",
		"PUT /api/v1/profile/me/avatar",
		"DELETE /api/v1/profile/me/avatar",
		"POST /api/v1/profile/me/email",
		"POST /api/v1/profile/me/email/confirm",
		"POST /api/v1/profile/me/password",
	})
}
