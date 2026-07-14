// Package middleware contains HTTP middleware helpers.
package middleware

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

const testSecret = "10+NIwWoxrsvQx4UyepsXSe4R2s2G+41hft5dM4Skw0="

type testJWT struct {
	sign    string
	role    string
	subject string
	expAt   time.Time
	issueAt time.Time
}

func (tj *testJWT) helperJWT() (string, error) {
	claims := AccessClaims{
		Role: tj.role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    JWTIssuer,
			Audience:  jwt.ClaimStrings{JWTAudience},
			Subject:   tj.subject,
			ExpiresAt: jwt.NewNumericDate(tj.expAt),
			IssuedAt:  jwt.NewNumericDate(tj.issueAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(tj.sign))
	if err != nil {
		return "", fmt.Errorf("generate access token: %w", err)
	}
	return tokenString, nil
}

func captureCtxHandler() (AppHandler, *uuid.UUID, *string) {
	var userID uuid.UUID
	var role string
	h := func(w http.ResponseWriter, r *http.Request) error {
		userID, _ = authctx.UserIDFromCtx(r.Context())
		role, _ = authctx.RoleFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
		return nil
	}
	return h, &userID, &role
}

func newTestRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(t.Context(), method, url, nil)
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	now := time.Now()

	userIDRaw, err := uuid.NewV7()
	assert.NoError(t, err)

	userID := userIDRaw.String()

	jwtData := testJWT{
		sign:    testSecret,
		role:    "defectologist",
		subject: userID,
		expAt:   now.Add(time.Duration(15) * time.Minute),
		issueAt: now,
	}

	token, err := jwtData.helperJWT()
	assert.NoError(t, err)

	req := newTestRequest(t, "GET", "/test")
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()

	authJWTNoop, gotUserID, gotRole := captureCtxHandler()

	au := NewAuthMW([]byte(jwtData.sign))
	handler := ErrorMiddleware(au.AuthMiddleware(authJWTNoop))

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, userID, gotUserID.String())
	assert.Equal(t, "defectologist", *gotRole)
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	now := time.Now()

	userIDRaw, err := uuid.NewV7()
	assert.NoError(t, err)

	jwtData := testJWT{
		sign:    testSecret,
		role:    "defectologist",
		subject: userIDRaw.String(),
		expAt:   now.Add(-time.Hour),
		issueAt: now.Add(-2 * time.Hour),
	}

	token, err := jwtData.helperJWT()
	assert.NoError(t, err)

	req := newTestRequest(t, "GET", "/test")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler, _, _ := captureCtxHandler()
	au := NewAuthMW([]byte(jwtData.sign))
	h := ErrorMiddleware(au.AuthMiddleware(handler))
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "JWT_TOKEN_INVALID", resp.Error.Code)
}

func TestAuthMiddleware_NoExpiration(t *testing.T) {
	claims := AccessClaims{
		Role: "defectologist",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   JWTIssuer,
			Audience: jwt.ClaimStrings{JWTAudience},
			Subject:  uuid.NewString(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testSecret))
	assert.NoError(t, err)

	req := newTestRequest(t, "GET", "/test")
	req.Header.Set("Authorization", "Bearer "+signed)
	rec := httptest.NewRecorder()

	handler, _, _ := captureCtxHandler()
	au := NewAuthMW([]byte(testSecret))
	h := ErrorMiddleware(au.AuthMiddleware(handler))
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_WrongIssuer(t *testing.T) {
	claims := AccessClaims{
		Role: "defectologist",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "some-other-service",
			Audience:  jwt.ClaimStrings{JWTAudience},
			Subject:   uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testSecret))
	assert.NoError(t, err)

	req := newTestRequest(t, "GET", "/test")
	req.Header.Set("Authorization", "Bearer "+signed)
	rec := httptest.NewRecorder()

	handler, _, _ := captureCtxHandler()
	au := NewAuthMW([]byte(testSecret))
	h := ErrorMiddleware(au.AuthMiddleware(handler))
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
func TestAuthMiddleware_NoAuthHeader(t *testing.T) {
	req := newTestRequest(t, "GET", "/test")
	// заголовок Authorization не выставляется вообще
	rec := httptest.NewRecorder()

	handler, _, _ := captureCtxHandler()
	au := NewAuthMW([]byte(testSecret))
	h := ErrorMiddleware(au.AuthMiddleware(handler))
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_EmptyBearerToken(t *testing.T) {
	req := newTestRequest(t, "GET", "/test")
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()

	handler, _, _ := captureCtxHandler()
	au := NewAuthMW([]byte(testSecret))
	h := ErrorMiddleware(au.AuthMiddleware(handler))
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_WrongSignature(t *testing.T) {
	claims := AccessClaims{
		Role: "defectologist",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    JWTIssuer,
			Audience:  jwt.ClaimStrings{JWTAudience},
			Subject:   uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("completely-different-wrong-secret-key"))
	assert.NoError(t, err)

	req := newTestRequest(t, "GET", "/test")
	req.Header.Set("Authorization", "Bearer "+signed)
	rec := httptest.NewRecorder()

	handler, _, _ := captureCtxHandler()
	au := NewAuthMW([]byte(testSecret)) // валидирует другим секретом
	h := ErrorMiddleware(au.AuthMiddleware(handler))
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_MissingSubject(t *testing.T) {
	claims := AccessClaims{
		Role: "defectologist",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    JWTIssuer,
			Audience:  jwt.ClaimStrings{JWTAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			// Subject не заполнен
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testSecret))
	assert.NoError(t, err)

	req := newTestRequest(t, "GET", "/test")
	req.Header.Set("Authorization", "Bearer "+signed)
	rec := httptest.NewRecorder()

	handler, _, _ := captureCtxHandler()
	au := NewAuthMW([]byte(testSecret))
	h := ErrorMiddleware(au.AuthMiddleware(handler))
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_MalformedTokenStructure(t *testing.T) {
	testCases := []struct {
		name  string
		token string
	}{
		{"not base64", "not.a.valid.jwt.token"},
		{"single part", "onlyonepart"},
		{"empty string", ""},
		{"two parts only", "header.payload"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := newTestRequest(t, "GET", "/test")
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()

			handler, _, _ := captureCtxHandler()
			au := NewAuthMW([]byte(testSecret))
			h := ErrorMiddleware(au.AuthMiddleware(handler))
			h.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
}

func TestAuthMiddleware_AlgNone(t *testing.T) {
	// Ручная сборка токена с alg:none, обходя стандартный jwt.NewWithClaims,
	// чтобы точно проверить именно защиту от algorithm confusion в самом middleware.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims := fmt.Sprintf(`{"sub":"%s","role":"defectologist","iss":"zip-backend","exp":%d}`,
		uuid.NewString(), time.Now().Add(time.Hour).Unix())
	payload := base64.RawURLEncoding.EncodeToString([]byte(claims))
	maliciousToken := header + "." + payload + "."

	req := newTestRequest(t, "GET", "/test")
	req.Header.Set("Authorization", "Bearer "+maliciousToken)
	rec := httptest.NewRecorder()

	handler, _, _ := captureCtxHandler()
	au := NewAuthMW([]byte(testSecret))
	h := ErrorMiddleware(au.AuthMiddleware(handler))
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
func TestAuthMiddleware_WrongAudience(t *testing.T) {
	claims := AccessClaims{
		Role: "defectologist",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    JWTIssuer,
			Audience:  jwt.ClaimStrings{"some-other-audience"},
			Subject:   uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testSecret))
	assert.NoError(t, err)

	req := newTestRequest(t, "GET", "/test")
	req.Header.Set("Authorization", "Bearer "+signed)
	rec := httptest.NewRecorder()

	handler, _, _ := captureCtxHandler()
	au := NewAuthMW([]byte(testSecret))
	h := ErrorMiddleware(au.AuthMiddleware(handler))
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
