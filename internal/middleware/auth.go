// Package middleware contains HTTP middleware helpers.
package middleware

import (
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
}

type AccessClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

func NewAuthMW(secret []byte) *AuthMW {
	return &AuthMW{jwtSecret: secret}
}

func (m *AuthMW) AuthMiddleware(next AppHandler) AppHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			return apperr.ErrJWTTokenInvalid
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			return apperr.ErrJWTTokenInvalid
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		token, err := jwt.ParseWithClaims(tokenString, &AccessClaims{}, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("auth unexpected signing method: %v", t.Header["alg"])
			}
			return m.jwtSecret, nil
		},
			jwt.WithExpirationRequired(),
			jwt.WithIssuer(JWTIssuer),
			jwt.WithAudience(JWTAudience),
			jwt.WithIssuedAt(),
			jwt.WithLeeway(10*time.Second),
		)
		if errors.Is(err, jwt.ErrTokenExpired) {
			return apperr.ErrJWTTokenInvalid.WithError(fmt.Errorf("auth: token expired: %w", err))
		}
		if err != nil {
			return apperr.ErrJWTTokenInvalid.WithError(fmt.Errorf("auth: token validation failed: %w", err))
		}

		claims, ok := token.Claims.(*AccessClaims)
		if !ok {
			return apperr.ErrJWTTokenInvalid.WithError(fmt.Errorf("auth: unexpected claims type %T", token.Claims))
		}

		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			return apperr.ErrJWTTokenInvalid.WithError(fmt.Errorf("auth parse sub: %w", err))
		}

		ctx := authctx.SetUserIDToCtx(r.Context(), userID)
		ctx = authctx.SetRoleToCtx(ctx, claims.Role)
		return next(w, r.WithContext(ctx))
	}
}
