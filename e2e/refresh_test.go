//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
	"golang.org/x/crypto/bcrypt"

	"github.com/Linka-masterskaya/zip-backend/internal/auth"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

const (
	e2eAuthEmail    = "refresh@example.com"
	e2eAuthPassword = "correct-password"
)

var e2eAuthSecret = strings.Repeat("e", 40)

func TestE2E_RefreshTokenLifecycle(t *testing.T) {
	pool := e2eDatabase(t)
	crypto, err := cryptox.New(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	userID := e2eAuthUser(t, pool, crypto)

	redisContainer, err := rediscontainer.Run(t.Context(), "redis:7-alpine")
	testcontainers.CleanupContainer(t, redisContainer)
	require.NoError(t, err)
	redisURL, err := redisContainer.ConnectionString(t.Context())
	require.NoError(t, err)
	refreshCache, err := cache.NewClient(cache.Config{
		URL: redisURL, ClientName: "refresh-e2e", PoolSize: 5,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, refreshCache.Close()) })

	authConfig := auth.Config{
		JWTSecret:                e2eAuthSecret,
		AccessTokenTTL:           15 * time.Minute,
		RefreshTokenTTL:          time.Hour,
		RequireEmailVerification: true,
	}
	authService := auth.NewAuthService(
		auth.NewAuthRepo(pool), refreshCache, nil, authConfig, crypto,
	)
	authHandler := auth.NewHandler(authService, authConfig)
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/auth/login", middleware.ErrorMiddleware(authHandler.Login))
	mux.Handle("POST /api/v1/auth/refresh", middleware.ErrorMiddleware(authHandler.Refresh))
	server := httptest.NewServer(middleware.Chain(
		mux, middleware.RecoveryMiddleware, middleware.RequestIDMiddleware,
	))
	t.Cleanup(server.Close)

	firstRefresh := e2eLogin(t, server)
	secondRefresh := e2eRefresh(t, server, firstRefresh, http.StatusOK)
	require.NotEqual(t, firstRefresh.Value, secondRefresh.Value)

	// Reusing an already rotated token revokes the complete token family.
	e2eRefresh(t, server, firstRefresh, http.StatusUnauthorized)
	e2eRefresh(t, server, secondRefresh, http.StatusUnauthorized)

	// A newly logged-in family is invalidated by logout-all/session revocation.
	thirdRefresh := e2eLogin(t, server)
	require.NoError(t, refreshCache.RevokeAllSessions(t.Context(), userID.String()))
	e2eRefresh(t, server, thirdRefresh, http.StatusUnauthorized)

	// Sessions created after logout-all use the new version and can rotate.
	fourthRefresh := e2eLogin(t, server)
	fifthRefresh := e2eRefresh(t, server, fourthRefresh, http.StatusOK)

	// Soft-deleted users cannot refresh an otherwise valid session.
	_, err = pool.Exec(t.Context(), `UPDATE users SET deleted_at = now() WHERE id = $1`, userID)
	require.NoError(t, err)
	e2eRefresh(t, server, fifthRefresh, http.StatusUnauthorized)
}

func e2eAuthUser(t *testing.T, pool *pgxpool.Pool, crypto *cryptox.Cryptox) uuid.UUID {
	t.Helper()
	orgID := uuid.New()
	userID := uuid.New()
	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(e2eAuthPassword), bcrypt.MinCost,
	)
	require.NoError(t, err)
	emailHash := crypto.Hash([]byte(e2eAuthEmail))
	emailEncrypted, err := crypto.Encrypt([]byte(e2eAuthEmail))
	require.NoError(t, err)

	_, err = pool.Exec(t.Context(), `
		INSERT INTO organizations (id, name) VALUES ($1, 'Refresh E2E')
	`, orgID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO users (id, org_id, email_verified, display_name)
		VALUES ($1, $2, true, 'Refresh User')
	`, userID, orgID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO auth_cred (user_id, email_hash, email_encrypted, password_hash, role)
		VALUES ($1, $2, $3, $4, 'defectologist')
	`, userID, emailHash, emailEncrypted, string(passwordHash))
	require.NoError(t, err)
	return userID
}

func e2eLogin(t *testing.T, server *httptest.Server) *http.Cookie {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"email": e2eAuthEmail, "password": e2eAuthPassword,
	})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, server.URL+"/api/v1/auth/login", bytes.NewReader(body),
	)
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer e2eClose(t, response)
	require.Equal(t, http.StatusOK, response.StatusCode)
	return e2eRefreshCookie(t, response)
}

func e2eRefresh(
	t *testing.T,
	server *httptest.Server,
	cookie *http.Cookie,
	expectedStatus int,
) *http.Cookie {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, server.URL+"/api/v1/auth/refresh", nil,
	)
	require.NoError(t, err)
	request.AddCookie(cookie)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer e2eClose(t, response)
	require.Equal(t, expectedStatus, response.StatusCode)
	if expectedStatus != http.StatusOK {
		return nil
	}
	return e2eRefreshCookie(t, response)
}

func e2eRefreshCookie(t *testing.T, response *http.Response) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == "refresh_token" {
			require.NotEmpty(t, cookie.Value)
			return cookie
		}
	}
	t.Fatal("refresh_token cookie is missing")
	return nil
}
