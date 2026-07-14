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

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrEmailNotVerified = errors.New("email not verified")

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

	beginTx(ctx context.Context) (pgx.Tx, error)
	withTx(tx pgx.Tx) authRepoIface
	useEmailVerifyToken(ctx context.Context, token []byte) (uuid.UUID, uuid.UUID, error)
	verifyUser(ctx context.Context, userID uuid.UUID) error
	verifyStudent(ctx context.Context, studentID uuid.UUID) error
	getUserContactForResend(ctx context.Context, userID uuid.UUID) ([]byte, bool, error)
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
	RequireEmailVerification bool
	CookieSecure             bool
}

type LoginResult struct {
	AccessToken  string
	RefreshToken string
}

type authService struct {
	repo   authRepoIface
	cache  refreshStore
	mailer domain.EmailSender
	cfg    Config
	crp    cryptoService
}

func NewAuthService(
	repo authRepoIface,
	cache refreshStore,
	mailer domain.EmailSender,
	cfg Config,
	crp cryptoService,
) *authService {
	return &authService{
		repo:   repo,
		cache:  cache,
		mailer: mailer,
		cfg:    cfg,
		crp:    crp,
	}
}

func (au *authService) Login(
	ctx context.Context,
	email, password string,
) (*LoginResult, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	emailHash := au.crp.Hash([]byte(email))

	user, err := au.repo.GetUserByEmailHash(ctx, emailHash)
	if errors.Is(err, ErrUserNotFound) {
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

func (au *authService) resendEmail(ctx context.Context) error {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return err
	}

	emailEncrypted, emailVerified, err := au.repo.getUserContactForResend(ctx, userID)
	if err != nil {
		return err
	}
	if emailVerified {
		return nil
	}

	email, err := au.crp.Decrypt(emailEncrypted)
	if err != nil {
		return fmt.Errorf("authService.resendEmail: %w", err)
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
		string(email),
		domain.EmailVerify,
		domain.EmailData{
			Token: verifyURL,
		},
	)
	if err != nil {
		return fmt.Errorf("authService.resendEmail: %w", err)
	}

	return nil
}
