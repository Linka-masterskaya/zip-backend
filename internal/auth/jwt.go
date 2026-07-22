package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	jwtIssuer   = "zip-backend"
	jwtAudience = "zip-backend-api"
)

type AccessClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

type RefreshClaims struct {
	jwt.RegisteredClaims
}

func (au *authService) generateAccessToken(user *User) (string, error) {
	now := time.Now()

	claims := AccessClaims{
		Role: user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			Issuer:    jwtIssuer,
			Audience:  jwt.ClaimStrings{jwtAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(au.cfg.AccessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(au.cfg.JWTSecret))
	if err != nil {
		return "", fmt.Errorf("generate access token: %w", err)
	}

	return tokenString, nil
}

func (au *authService) parseRefreshToken(tokenString string) (*RefreshClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &RefreshClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("refresh unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(au.cfg.JWTSecret), nil
	},
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(jwtIssuer),
		jwt.WithAudience(jwtAudience),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("parse refresh token: %w", err)
	}

	claims, ok := token.Claims.(*RefreshClaims)
	if !ok {
		return nil, fmt.Errorf("parse refresh token: unexpected claims type %T", token.Claims)
	}

	return claims, nil
}

func (au *authService) generateRefreshToken(user *User, jti string) (string, error) {
	now := time.Now()

	claims := RefreshClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			Issuer:    jwtIssuer,
			Audience:  jwt.ClaimStrings{jwtAudience},
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(now.Add(au.cfg.RefreshTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(au.cfg.JWTSecret))
	if err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}

	return tokenString, nil
}
