package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

//go:generate mockgen -source=service.go -destination=mock_repo_test.go -package=auth
type authRepoIface interface {
	beginTx(ctx context.Context) (pgx.Tx, error)
	withTx(tx pgx.Tx) authRepoIface
	useEmailVerifyToken(ctx context.Context, token []byte) (uuid.UUID, uuid.UUID, error)
	verifyUser(ctx context.Context, userID uuid.UUID) error
	verifyStudent(ctx context.Context, studentID uuid.UUID) error
	getUserContactForResend(ctx context.Context, userID uuid.UUID) ([]byte, bool, error)
	rotateEmailTokens(ctx context.Context, tokenID, userID uuid.UUID, hash []byte, expiresAt time.Time) error
}

type Config struct {
	FrontendURL              string
	AccessTokenTTL           time.Duration
	RefreshTokenTTL          time.Duration
	VerifyEmailTokenTTL      time.Duration
	RequireEmailVerification bool
}

type authService struct {
	repo   authRepoIface
	cache  *cache.Client
	mailer domain.EmailSender
	cfg    Config
	crp    *cryptox.Cryptox
}

func NewAuthService(repo authRepoIface, cache *cache.Client, mailer domain.EmailSender, cfg Config, crp *cryptox.Cryptox) authServiceIface {
	return &authService{repo: repo, cache: cache, mailer: mailer, cfg: cfg, crp: crp}
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

	err = au.repo.rotateEmailTokens(ctx, tokenID, userID, hashToken[:], time.Now().Add(au.cfg.VerifyEmailTokenTTL))
	if err != nil {
		return fmt.Errorf("authService.resendEmail: %w", err)
	}

	verifyURL := au.cfg.FrontendURL + "/verify-email?token=" + base64.RawURLEncoding.EncodeToString(tokenRaw)

	err = au.mailer.Send(ctx, string(email), domain.EmailVerify, domain.EmailData{
		Token: verifyURL,
	})
	if err != nil {
		return fmt.Errorf("authService.resendEmail: %w", err)
	}

	return nil
}
