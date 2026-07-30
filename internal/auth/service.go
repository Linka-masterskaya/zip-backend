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
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials     = errors.New("invalid credentials")
	ErrEmailNotVerified       = errors.New("email not verified")
	ErrEmailAlreadyRegistered = errors.New("email already registered")
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
	GetUserByID(ctx context.Context, userID uuid.UUID) (*User, error)
	GetAuthCredByUserID(ctx context.Context, userID uuid.UUID) (*UserCred, error)
	FindIdentityByProviderUID(ctx context.Context, provider, providerUID string) (*UserIdentity, error)
	CreateOAuthUser(ctx context.Context, params CreateUserParams) error
	UpdateUserName(ctx context.Context, userID uuid.UUID, name string) error
	CreateIdentity(ctx context.Context, identity *UserIdentity) error
	CreateAuthCred(ctx context.Context, params CreateAuthCredParams) error
	beginTx(ctx context.Context) (pgx.Tx, error)
	withTx(tx pgx.Tx) authRepoIface
	useEmailVerifyToken(ctx context.Context, token []byte) (uuid.UUID, uuid.UUID, error)
	verifyUser(ctx context.Context, userID uuid.UUID) error
	verifyStudent(ctx context.Context, studentID uuid.UUID) error
	getUserContactForResend(ctx context.Context, userID uuid.UUID) ([]byte, bool, error)
	rotateEmailTokens(ctx context.Context, tokenID, userID uuid.UUID, hash []byte, expiresAt time.Time) error
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
	Encrypt(plaintext []byte) ([]byte, error)
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
	mailer mailer.EmailSender
	cfg    Config
	crp    cryptoService
}

func NewAuthService(
	repo authRepoIface,
	cache refreshStore,
	mailer mailer.EmailSender,
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

func (au *authService) GenerateOAuthJWT(user *User, cred *UserCred) (string, error) {
	email, err := au.crp.Decrypt(cred.EmailEncrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt email: %w", err)
	}

	now := time.Now()

	claims := jwt.MapClaims{
		"sub":   user.ID,
		"email": string(email),
		"role":  user.Role,
		"iss":   jwtIssuer,
		"aud":   jwtAudience,
		"exp":   now.Add(24 * time.Hour).Unix(),
		"iat":   now.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(au.cfg.JWTSecret))
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	return tokenString, nil
}

// UpsertUser creates or updates user from OAuth provider
func (au *authService) UpsertUser(ctx context.Context, email, name, yandexID string) (*User, *UserCred, error) {
	identity, err := au.repo.FindIdentityByProviderUID(ctx, "yandex", yandexID)
	if err != nil {
		return nil, nil, fmt.Errorf("find identity: %w", err)
	}
	if identity != nil {
		return au.handleExistingIdentity(ctx, identity, name)
	}

	emailHash := au.crp.Hash([]byte(email))
	user, err := au.repo.GetUserByEmailHash(ctx, emailHash)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return nil, nil, fmt.Errorf("get user by email: %w", err)
	}
	if user != nil {
		slog.Warn("attempt to takeover existing email via yandex oauth",
			"user_id", user.ID,
			"yandex_id", yandexID,
			"email_hash", fmt.Sprintf("%x", emailHash),
		)
		return nil, nil, ErrEmailAlreadyRegistered
	}

	return au.createOAuthUser(ctx, email, name, yandexID)
}

// handleExistingIdentity processes existing identity
func (au *authService) handleExistingIdentity(ctx context.Context, identity *UserIdentity, name string) (*User, *UserCred, error) {
	user, err := au.repo.GetUserByID(ctx, identity.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("get user by id: %w", err)
	}
	if user == nil {
		return nil, nil, fmt.Errorf("user not found for identity")
	}

	if err := au.repo.UpdateUserName(ctx, identity.UserID, name); err != nil {
		return nil, nil, fmt.Errorf("update user name: %w", err)
	}

	cred, err := au.repo.GetAuthCredByUserID(ctx, identity.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("get auth_cred: %w", err)
	}
	if cred == nil {
		return nil, nil, fmt.Errorf("auth_cred not found")
	}

	return user, cred, nil
}

// createOAuthUser creates new user from OAuth provider
func (au *authService) createOAuthUser(ctx context.Context, email, name, yandexID string) (*User, *UserCred, error) {
	tx, err := au.repo.beginTx(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Warn("failed to rollback transaction", "error", err)
		}
	}()
	txRepo := au.repo.withTx(tx)

	userID, err := uuid.NewV7()
	if err != nil {
		return nil, nil, fmt.Errorf("generate user id: %w", err)
	}

	if err := txRepo.CreateOAuthUser(ctx, CreateUserParams{
		ID:             userID,
		OrganizationID: nil,
		Name:           name,
		EmailVerified:  true,
	}); err != nil {
		return nil, nil, fmt.Errorf("create user: %w", err)
	}

	emailHash := au.crp.Hash([]byte(email))
	emailEncrypted, err := au.crp.Encrypt([]byte(email))
	if err != nil {
		return nil, nil, fmt.Errorf("encrypt email: %w", err)
	}

	if err := txRepo.CreateAuthCred(ctx, CreateAuthCredParams{
		UserID:         userID,
		EmailHash:      emailHash,
		EmailEncrypted: emailEncrypted,
		PasswordHash:   "",
		Role:           "defectologist",
	}); err != nil {
		return nil, nil, fmt.Errorf("create auth_cred: %w", err)
	}

	if err := txRepo.CreateIdentity(ctx, &UserIdentity{
		ID:          uuid.New(),
		UserID:      userID,
		Provider:    "yandex",
		ProviderUID: yandexID,
	}); err != nil {
		return nil, nil, fmt.Errorf("create identity: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit tx: %w", err)
	}

	user, err := au.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("get created user: %w", err)
	}

	cred, err := au.repo.GetAuthCredByUserID(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("get created auth_cred: %w", err)
	}

	return user, cred, nil
}

// SendVerificationEmail sends email verification for OAuth user
func (au *authService) SendVerificationEmail(ctx context.Context, userID uuid.UUID) error {
	cred, err := au.repo.GetAuthCredByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get auth_cred: %w", err)
	}
	if cred == nil {
		return fmt.Errorf("auth_cred not found")
	}

	email, err := au.crp.Decrypt(cred.EmailEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt email: %w", err)
	}

	tokenRaw := make([]byte, 32)
	if _, err := rand.Read(tokenRaw); err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	tokenHash := sha256.Sum256(tokenRaw)

	tokenID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate token id: %w", err)
	}

	if err := au.repo.rotateEmailTokens(
		ctx,
		tokenID,
		userID,
		tokenHash[:],
		time.Now().Add(au.cfg.VerifyEmailTokenTTL),
	); err != nil {
		return fmt.Errorf("rotate email tokens: %w", err)
	}

	verifyURL := au.cfg.FrontendURL +
		"/verify-email?token=" +
		base64.RawURLEncoding.EncodeToString(tokenRaw)

	if err := au.mailer.Send(
		ctx,
		string(email),
		mailer.EmailVerify,
		mailer.EmailData{
			Token: verifyURL,
		},
	); err != nil {
		return fmt.Errorf("send email: %w", err)
	}

	slog.Info("verification email sent", "user_id", userID)
	return nil
}
