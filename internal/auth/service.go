package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
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

//go:generate mockgen -source=service.go -destination=mock_repo_test.go -package=auth
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
}

type refreshStore interface {
	StoreRefresh(
		ctx context.Context,
		jti string,
		rec cache.RefreshRecord,
		ttl time.Duration,
	) error
	RevokeAllSessions(ctx context.Context, userID string) error
}

type rateLimit interface {
	Allow(ctx context.Context, req cache.RateLimitRequest) (bool, int64, error)
}

type cryptoService interface {
	Hash(data []byte) []byte
	Decrypt(ciphertext []byte) ([]byte, error)
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
	RateLimit                config.RateLimitConfig
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

func NewAuthService(
	repo authRepoIface,
	cache refreshStore,
	rlCache rateLimit,
	mailer mailer.EmailSender,
	cfg Config,
	crp cryptoService,
) *authService {
	return &authService{
		repo:    repo,
		rlCache: rlCache,
		cache:   cache,
		mailer:  mailer,
		cfg:     cfg,
		crp:     crp,
	}
}

func (au *authService) Login(
	ctx context.Context,
	email, password string,
) (*LoginResult, error) {
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

func (au *authService) verifyEmail(
	ctx context.Context,
	verifyToken string,
) error {
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

func (au *authService) resendEmail(ctx context.Context, strEmail string) error {
	strEmail = strings.TrimSpace(strings.ToLower(strEmail))
	emailHash := au.crp.Hash([]byte(strEmail))

	rlCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	hashKey := hex.EncodeToString(emailHash)
	allowed, _, err := au.rlCache.Allow(rlCtx, cache.RateLimitRequest{
		Scope: au.cfg.RateLimit.Resend.Scope, Key: hashKey,
		Limit: au.cfg.RateLimit.Resend.Limit, WindowSize: au.cfg.RateLimit.Resend.Window,
	})
	if err != nil {
		return apperr.ErrInternal.WithError(fmt.Errorf("cache.Allow: %w", err))
	}
	if !allowed {
		return apperr.ErrTooManyRequests
	}

	user, err := au.repo.GetUserByEmailHash(ctx, emailHash)
	if errors.Is(err, apperr.ErrUserNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if user.EmailVerified {
		return nil
	}

	tokenRaw := make([]byte, 32)
	if _, err := rand.Read(tokenRaw); err != nil {
		return fmt.Errorf("authService.resendEmail: %w", err)
	}

	hashToken := sha256.Sum256(tokenRaw)

	tokenID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("authService.resendEmail: %w", err)
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		return fmt.Errorf("authService.resendEmail: %w", err)
	}

	err = au.repo.rotateEmailTokens(
		ctx,
		tokenID,
		userID,
		hashToken[:],
		time.Now().Add(au.cfg.VerifyEmailTokenTTL),
	)
	if err != nil {
		return fmt.Errorf("authService.resendEmail: %w", err)
	}

	verifyURL := au.cfg.FrontendURL +
		"/verify-email?token=" +
		base64.RawURLEncoding.EncodeToString(tokenRaw)

	err = au.mailer.Send(
		ctx,
		strEmail,
		mailer.EmailVerify,
		mailer.EmailData{
			Token: verifyURL,
		},
	)
	if err != nil {
		return fmt.Errorf("authService.resendEmail: %w", err)
	}

	return nil
}
