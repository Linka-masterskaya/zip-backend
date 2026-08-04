package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrEmailNotVerified   = errors.New("email not verified")
)

var dummyPasswordHash = []byte("$2a$10$UlCQgLZoLjUzrtYRUUlkPeh/m5L2pl9aYzDTUaZAD3R4Pd8ONSof6")

// runDummyPasswordCompare performs a bcrypt comparison only to keep the
// execution time similar for existing and non-existing users.
//
//nolint:errcheck // result is intentionally ignored for timing consistency.
func runDummyPasswordCompare(password string) {
	_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
}

//go:generate go run go.uber.org/mock/mockgen -source=service.go -destination=mock_repo_test.go -package=auth
type authRepoIface interface {
	GetUserByEmailHash(ctx context.Context, emailHash []byte) (*User, error)
	CreatePasswordResetToken(ctx context.Context, userID string, ttl time.Duration) (string, error)
	ResetPasswordByToken(ctx context.Context, token string, passwordHash string) (string, error)

	beginTx(ctx context.Context) (pgx.Tx, error)
	withTx(tx pgx.Tx) authRepoIface
	useEmailVerifyToken(ctx context.Context, token []byte) (uuid.UUID, uuid.UUID, error)
	verifyUser(ctx context.Context, userID uuid.UUID) error
	verifyStudent(ctx context.Context, studentID uuid.UUID) error
	rotateEmailTokens(
		ctx context.Context,
		tokenID, userID uuid.UUID,
		hash []byte,
		expiresAt time.Time,
	) error

	GetUserByID(ctx context.Context, userID uuid.UUID) (*User, error)
}

type refreshStore interface {
	StoreRefresh(
		ctx context.Context,
		jti string,
		rec cache.RefreshRecord,
		ttl time.Duration,
	) error

	GetRefresh(
		ctx context.Context,
		jti string,
	) (*cache.RefreshRecord, error)

	RevokeFamily(
		ctx context.Context,
		fid string,
	) error

	IsFamilyRevoked(
		ctx context.Context,
		fid string,
	) (bool, error)

	IsSessionRevoked(
		ctx context.Context,
		rec cache.RefreshRecord,
	) (bool, error)

	RotateRefresh(
		ctx context.Context,
		req cache.RotateRefreshRequest,
	) error

	RevokeAllSessions(ctx context.Context, userID string) error
}

type cryptoService interface {
	Hash(data []byte) []byte
	Decrypt(ciphertext []byte) ([]byte, error)
}

type rateLimit interface {
	Allow(ctx context.Context, req cache.RateLimitRequest) (bool, int64, error)
}

type Config struct {
	JWTSecret                string
	FrontendURL              string
	AccessTokenTTL           time.Duration
	RefreshTokenTTL          time.Duration
	VerifyEmailTokenTTL      time.Duration
	ResetPasswordTokenTTL    time.Duration
	BcryptCost               int
	RequireEmailVerification bool
	CookieSecure             bool
	RateLimit                middleware.RateLimitPolicy
}

type LoginResult struct {
	AccessToken  string
	RefreshToken string
}

type authService struct {
	repo    authRepoIface
	cache   refreshStore
	rlCache rateLimit
	mailer  mailer.EmailSender
	cfg     Config
	crp     cryptoService
}

func NewAuthService(repo authRepoIface, cache refreshStore, rlCache rateLimit, mailer mailer.EmailSender, cfg Config, crp cryptoService) *authService {
	return &authService{
		repo:    repo,
		cache:   cache,
		rlCache: rlCache,
		mailer:  mailer,
		cfg:     cfg,
		crp:     crp,
	}
}

func (au *authService) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	emailHash := au.crp.Hash([]byte(email))

	user, err := au.repo.GetUserByEmailHash(ctx, emailHash)
	if errors.Is(err, apperr.ErrUserNotFound) {
		runDummyPasswordCompare(password)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email hash: %w", err)
	}

	if user.PasswordHash == nil {
		runDummyPasswordCompare(password)
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword(
		[]byte(*user.PasswordHash),
		[]byte(password),
	); err != nil {
		return nil, ErrInvalidCredentials
	}

	if au.cfg.RequireEmailVerification && !user.EmailVerified {
		return nil, ErrEmailNotVerified
	}

	accessToken, err := au.generateAccessToken(user)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	jti := uuid.NewString()
	fid := uuid.NewString()

	refreshToken, err := au.generateRefreshToken(user, jti)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	rec := cache.RefreshRecord{
		FID:    fid,
		Status: "active",
		UserID: user.ID,
	}

	if err := au.cache.StoreRefresh(
		ctx,
		jti,
		rec,
		au.cfg.RefreshTokenTTL,
	); err != nil {
		return nil, fmt.Errorf("store refresh token: %w", err)
	}

	return &LoginResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func (au *authService) verifyEmail(ctx context.Context, verifyToken string) error {
	raw, err := base64.RawURLEncoding.DecodeString(verifyToken)
	if err != nil {
		return apperr.ErrVerifyTokenInvalid
	}

	tokenHash := sha256.Sum256(raw)
	token := tokenHash[:]

	tx, err := au.repo.beginTx(ctx)
	if err != nil {
		return fmt.Errorf("authService.verifyEmail: %w", err)
	}

	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Error("tx rollback failed", "err", err)
		}
	}()

	txRepo := au.repo.withTx(tx)

	userID, studentID, err := txRepo.useEmailVerifyToken(ctx, token)
	if err != nil {
		return err
	}

	switch {
	case userID != uuid.Nil:
		err = txRepo.verifyUser(ctx, userID)
	case studentID != uuid.Nil:
		err = txRepo.verifyStudent(ctx, studentID)
	default:
		return fmt.Errorf("authService.verifyEmail: token has no owner")
	}
	if err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("authService.verifyEmail: %w", err)
	}

	return nil
}

// resendEmail повторно отправляет письмо верификации по адресу, а не по JWT:
// неподтверждённый пользователь может не иметь возможности войти, и тогда
// запросить письмо было бы нечем.
func (au *authService) resendEmail(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		return err
	}

	emailHash := au.crp.Hash([]byte(email))

	user, err := au.repo.GetUserByEmailHash(ctx, emailHash)
	if err != nil {
		// Ответ одинаков для существующего и несуществующего адреса.
		if errors.Is(err, apperr.ErrUserNotFound) {
			return nil
		}
		return err
	}
	if user.EmailVerified {
		return nil
	}

	rlCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	allowed, _, err := au.rlCache.Allow(rlCtx, cache.RateLimitRequest{
		Scope: au.cfg.RateLimit.Scope, Key: user.ID,
		Limit: au.cfg.RateLimit.Limit, WindowSize: au.cfg.RateLimit.Window,
	})
	if err != nil {
		return apperr.ErrInternal.WithError(fmt.Errorf("cache.Allow: %w", err))
	}
	if !allowed {
		return apperr.ErrTooManyRequests
	}

	go au.processResend(context.WithoutCancel(ctx), user.ID, email)

	return nil
}

func (au *authService) processResend(ctx context.Context, strUser string, email string) {
	userID, err := uuid.Parse(strUser)
	if err != nil {
		slog.ErrorContext(ctx, "authService.resendEmail",
			"user_id", strUser,
			logger.Err(err),
		)
		return
	}

	tokenRaw := make([]byte, 32)
	if _, err := rand.Read(tokenRaw); err != nil {
		slog.ErrorContext(ctx, "authService.resendEmail",
			"user_id", userID,
			logger.Err(err),
		)
		return
	}

	hashToken := sha256.Sum256(tokenRaw)

	tokenID, err := uuid.NewV7()
	if err != nil {
		slog.ErrorContext(ctx, "authService.resendEmail",
			"user_id", userID,
			logger.Err(err),
		)
		return
	}

	dbCtx, dbCancel := context.WithTimeout(ctx, 2*time.Second)
	defer dbCancel()
	err = au.repo.rotateEmailTokens(
		dbCtx,
		tokenID,
		userID,
		hashToken[:],
		time.Now().Add(au.cfg.VerifyEmailTokenTTL),
	)
	if err != nil {
		slog.ErrorContext(ctx, "authService.resendEmail",
			"user_id", userID,
			logger.Err(err),
		)
		return
	}

	token := base64.RawURLEncoding.EncodeToString(tokenRaw)

	mailCtx, mailCancel := context.WithTimeout(ctx, 10*time.Second)
	defer mailCancel()
	if err := au.mailer.Send(mailCtx, email, mailer.EmailVerify, mailer.EmailData{
		Token: token,
	}); err != nil {
		slog.ErrorContext(ctx, "authService.resendEmail",
			"user_id", userID,
			logger.Err(err),
		)
	}
}

func (au *authService) Refresh(ctx context.Context, refreshToken string) (*LoginResult, error) {
	claims, err := au.parseRefreshToken(refreshToken)
	if err != nil {
		return nil, apperr.ErrJWTTokenInvalid.WithError(err)
	}

	rec, err := au.getActiveRefreshRecord(ctx, claims.ID)
	if err != nil {
		return nil, err
	}

	user, err := au.getRefreshUser(ctx, claims.Subject)
	if err != nil {
		return nil, err
	}

	accessToken, err := au.generateAccessToken(user)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	newRefreshToken, err := au.rotateRefresh(ctx, claims.ID, rec, user)
	if err != nil {
		return nil, err
	}

	return &LoginResult{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
	}, nil
}

// Logout revokes the whole refresh token family. Идемпотентен: невалидный,
// истёкший или уже отозванный токен не считается ошибкой.
func (au *authService) Logout(ctx context.Context, refreshToken string) error {
	const op = "authService.Logout"

	if refreshToken == "" {
		return nil
	}

	claims, err := au.parseRefreshToken(refreshToken)
	if err != nil {
		return nil
	}

	rec, err := au.cache.GetRefresh(ctx, claims.ID)
	if errors.Is(err, cache.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	if err := au.cache.RevokeFamily(ctx, rec.FID); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func (au *authService) getActiveRefreshRecord(
	ctx context.Context,
	jti string,
) (*cache.RefreshRecord, error) {
	rec, err := au.cache.GetRefresh(ctx, jti)
	if errors.Is(err, cache.ErrNotFound) {
		return nil, apperr.ErrJWTTokenInvalid.WithError(err)
	}
	if err != nil {
		return nil, fmt.Errorf("get refresh token: %w", err)
	}

	if rec.Status == "revoked" {
		if err := au.cache.RevokeFamily(ctx, rec.FID); err != nil {
			return nil, fmt.Errorf("revoke refresh family: %w", err)
		}

		return nil, apperr.ErrJWTTokenInvalid
	}

	revoked, err := au.cache.IsFamilyRevoked(ctx, rec.FID)
	if err != nil {
		return nil, fmt.Errorf("check refresh family: %w", err)
	}
	if revoked {
		return nil, apperr.ErrJWTTokenInvalid
	}

	sessionRevoked, err := au.cache.IsSessionRevoked(ctx, *rec)
	if err != nil {
		return nil, fmt.Errorf("check refresh session: %w", err)
	}
	if sessionRevoked {
		return nil, apperr.ErrJWTTokenInvalid
	}

	return rec, nil
}

func (au *authService) getRefreshUser(ctx context.Context, subject string) (*User, error) {
	userID, err := uuid.Parse(subject)
	if err != nil {
		return nil, apperr.ErrJWTTokenInvalid.WithError(err)
	}

	user, err := au.repo.GetUserByID(ctx, userID)
	if errors.Is(err, apperr.ErrUserNotFound) {
		return nil, apperr.ErrUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}

	return user, nil
}

func (au *authService) rotateRefresh(
	ctx context.Context,
	oldJTI string,
	rec *cache.RefreshRecord,
	user *User,
) (string, error) {
	newJTI := uuid.NewString()

	newRefreshToken, err := au.generateRefreshToken(user, newJTI)
	if err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}

	req := cache.RotateRefreshRequest{
		OldJTI: oldJTI,
		NewJTI: newJTI,
		NewRecord: cache.RefreshRecord{
			FID:    rec.FID,
			Status: "active",
			UserID: rec.UserID,
		},
		TTL: au.cfg.RefreshTokenTTL,
	}

	err = au.cache.RotateRefresh(ctx, req)
	if errors.Is(err, cache.ErrRefreshNotActive) {
		if revokeErr := au.cache.RevokeFamily(ctx, rec.FID); revokeErr != nil {
			return "", fmt.Errorf("revoke refresh family: %w", revokeErr)
		}

		return "", apperr.ErrJWTTokenInvalid.WithError(err)
	}
	if errors.Is(err, cache.ErrNotFound) || errors.Is(err, cache.ErrSessionRevoked) {
		return "", apperr.ErrJWTTokenInvalid.WithError(err)
	}
	if err != nil {
		return "", fmt.Errorf("rotate refresh: %w", err)
	}

	return newRefreshToken, nil
}
