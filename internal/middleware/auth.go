// Package middleware contains HTTP middleware helpers.
package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	JWTIssuer   = "zip-backend"
	JWTAudience = "zip-backend-api"
)

type AuthMW struct {
	jwtSecret []byte
	sessions  SessionVersionStore
	users     ActiveUserStore
}

type AccessClaims struct {
	Role           string `json:"role"`
	SessionVersion int64  `json:"sess_ver"`
	jwt.RegisteredClaims
}

// SessionVersionStore provides the current user session generation. Bulk
// session revocation increments this value, invalidating both refresh and
// access tokens issued under an older generation.
type SessionVersionStore interface {
	GetUserSessionVersion(ctx context.Context, userID string) (int64, error)
}

// ActiveUserStore checks the authoritative PostgreSQL account state. It keeps
// soft-delete effective even if ephemeral session-revocation state is lost.
type ActiveUserStore interface {
	IsUserActive(ctx context.Context, userID uuid.UUID) (bool, error)
}

// WithActiveUserStore configures the authoritative account-state check used in
// production. It returns the receiver to keep server wiring concise.
func (m *AuthMW) WithActiveUserStore(users ActiveUserStore) *AuthMW {
	m.users = users
	return m
}

// NewAuthMW creates JWT authentication middleware. The optional session store
// keeps the constructor backwards-compatible for isolated tests while
// production supplies Redis and therefore validates access-token revocation.
func NewAuthMW(secret []byte, sessions ...SessionVersionStore) *AuthMW {
	mw := &AuthMW{jwtSecret: secret}
	if len(sessions) > 0 {
		mw.sessions = sessions[0]
	}
	return mw
}

func (m *AuthMW) AuthMiddleware(next AppHandler) AppHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		claims, userID, err := m.authenticateRequest(r)
		if err != nil {
			return err
		}
		if err := m.validateAccountState(r.Context(), userID, claims.SessionVersion); err != nil {
			return err
		}

		ctx := authctx.SetUserIDToCtx(r.Context(), userID)
		ctx = authctx.SetRoleToCtx(ctx, claims.Role)
		return next(w, r.WithContext(ctx))
	}
}

func (m *AuthMW) authenticateRequest(r *http.Request) (*AccessClaims, uuid.UUID, error) {
	tokenString, err := bearerToken(r.Header.Get("Authorization"))
	if err != nil {
		return nil, uuid.Nil, err
	}

	claims, err := m.parseAccessClaims(tokenString)
	if err != nil {
		return nil, uuid.Nil, err
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, uuid.Nil, apperr.ErrJWTTokenInvalid.WithError(fmt.Errorf("auth parse sub: %w", err))
	}
	return claims, userID, nil
}

func bearerToken(authHeader string) (string, error) {
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		return "", apperr.ErrJWTTokenInvalid
	}
	return strings.TrimPrefix(authHeader, "Bearer "), nil
}

func (m *AuthMW) parseAccessClaims(tokenString string) (*AccessClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &AccessClaims{}, m.accessTokenKey,
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(JWTIssuer),
		jwt.WithAudience(JWTAudience),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(10*time.Second),
	)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return nil, apperr.ErrJWTTokenInvalid.WithError(fmt.Errorf("auth: token expired: %w", err))
	}
	if err != nil {
		return nil, apperr.ErrJWTTokenInvalid.WithError(fmt.Errorf("auth: token validation failed: %w", err))
	}

	claims, ok := token.Claims.(*AccessClaims)
	if !ok {
		return nil, apperr.ErrJWTTokenInvalid.WithError(fmt.Errorf("auth: unexpected claims type %T", token.Claims))
	}
	return claims, nil
}

func (m *AuthMW) accessTokenKey(t *jwt.Token) (any, error) {
	if t.Method != jwt.SigningMethodHS256 {
		return nil, fmt.Errorf("auth unexpected signing method: %v", t.Header["alg"])
	}
	typ, ok := t.Header["typ"].(string)
	if !ok || typ != "access" {
		return nil, fmt.Errorf("auth unexpected token type: %v", t.Header["typ"])
	}
	return m.jwtSecret, nil
}

func (m *AuthMW) validateAccountState(ctx context.Context, userID uuid.UUID, sessionVersion int64) error {
	if err := m.validateActiveUser(ctx, userID); err != nil {
		return err
	}
	return m.validateSessionVersion(ctx, userID, sessionVersion)
}

func (m *AuthMW) validateActiveUser(ctx context.Context, userID uuid.UUID) error {
	if m.users == nil {
		return nil
	}
	active, err := m.users.IsUserActive(ctx, userID)
	if err != nil {
		return apperr.ErrInternal.WithError(fmt.Errorf("auth read account state: %w", err))
	}
	if !active {
		return apperr.ErrJWTTokenInvalid.WithError(fmt.Errorf("auth account is deleted or unavailable"))
	}
	return nil
}

func (m *AuthMW) validateSessionVersion(ctx context.Context, userID uuid.UUID, tokenVersion int64) error {
	if m.sessions == nil {
		return nil
	}
	currentVersion, err := m.sessions.GetUserSessionVersion(ctx, userID.String())
	if err != nil {
		return apperr.ErrInternal.WithError(fmt.Errorf("auth read session version: %w", err))
	}
	if tokenVersion != currentVersion {
		return apperr.ErrJWTTokenInvalid.WithError(fmt.Errorf(
			"auth session revoked: token version %d, current version %d",
			tokenVersion,
			currentVersion,
		))
	}
	return nil
}
