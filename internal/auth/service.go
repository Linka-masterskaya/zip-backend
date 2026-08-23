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
	CreateIdentity(ctx context.Context, identity *UserIdentity) error
	CreateAuthCred(ctx context.Context, params CreateAuthCredParams) error
	InvalidateAllVerifyTokens(ctx context.Context, userID uuid.UUID) error
	CreateOrganization(ctx context.Context, id uuid.UUID, name string) error
	CreatePasswordResetToken(ctx context.Context, userID string, ttl time.Duration) (string, error)
	ResetPasswordByToken(ctx context.Context, token string, passwordHash string) (uuid.UUID, error)
	beginTx(ctx context.Context) (pgx.Tx, error)
	withTx(tx pgx.Tx) authRepoIface
	useEmailVerifyToken(ctx context.Context, token []byte) (uuid.UUID, uuid.UUID, error)
	verifyUser(ctx context.Context, userID uuid.UUID) error
	verifyStudent(ctx context.Context, studentID uuid.UUID) error
	rotateEmailTokens(ctx context.Context, tokenID, userID uuid.UUID, hash []byte, expiresAt time.Time) error
}

type refreshStore interface {
	StoreRefresh(
		ctx context.Context,
		jti string,
		rec cache.RefreshRecord,
		ttl time.Duration,
	) error
	RevokeAllSessions(ctx context.Context, userID string) error
	GetRefresh(ctx context.Context, jti string) (*cache.RefreshRecord, error)
	RevokeFamily(ctx context.Context, fid string) error
	IsFamilyRevoked(ctx context.Context, fid string) (bool, error)
	IsSessionRevoked(ctx context.Context, rec cache.RefreshRecord) (bool, error)
	RotateRefresh(ctx context.Context, req cache.RotateRefreshRequest) error
	Allow(ctx context.Context, req cache.RateLimitRequest) (bool, int64, error)
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
	ResetPasswordTokenTTL    time.Duration
	RequireEmailVerification bool
	CookieSecure             bool
	BcryptCost               int
	RateLimit                RateLimitPolicy
}

type RateLimitPolicy struct {
	Scope  string
	Limit  int64
	Window time.Duration
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

// validateRefreshToken проверяет refresh токен и возвращает claims и запись из кэша.
func (au *authService) validateRefreshToken(ctx context.Context, refreshToken string) (*RefreshClaims, *cache.RefreshRecord, error) {
	token, err := jwt.ParseWithClaims(refreshToken, &RefreshClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		typ, ok := token.Header["typ"].(string)
		if !ok || typ != "refresh" {
			return nil, fmt.Errorf("unexpected token type: %v", token.Header["typ"])
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
		return nil, nil, apperr.ErrJWTTokenInvalid
	}

	claims, ok := token.Claims.(*RefreshClaims)
	if !ok || !token.Valid {
		return nil, nil, apperr.ErrJWTTokenInvalid
	}

	record, err := au.cache.GetRefresh(ctx, claims.ID)
	if err != nil || record == nil {
		return nil, nil, apperr.ErrJWTTokenInvalid
	}

	return claims, record, nil
}

// checkRefreshStatus проверяет статус refresh токена.
func (au *authService) checkRefreshStatus(ctx context.Context, record *cache.RefreshRecord) error {
	if record.Status == "revoked" {
		if err := au.cache.RevokeFamily(ctx, record.FID); err != nil {
			slog.ErrorContext(ctx, "failed to revoke family", "fid", record.FID, "error", err)
		}
		return apperr.ErrJWTTokenInvalid
	}

	isFamilyRevoked, err := au.cache.IsFamilyRevoked(ctx, record.FID)
	if err != nil {
		return apperr.ErrInternal.WithError(err)
	}
	if isFamilyRevoked {
		return apperr.ErrJWTTokenInvalid
	}

	isSessionRevoked, err := au.cache.IsSessionRevoked(ctx, *record)
	if err != nil {
		return apperr.ErrInternal.WithError(err)
	}
	if isSessionRevoked {
		return apperr.ErrJWTTokenInvalid
	}

	return nil
}

// getUserFromRefresh получает пользователя по subject из claims.
func (au *authService) getUserFromRefresh(ctx context.Context, subject string) (*User, error) {
	userID, err := uuid.Parse(subject)
	if err != nil {
		return nil, apperr.ErrJWTTokenInvalid
	}

	user, err := au.repo.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		return nil, apperr.ErrJWTTokenInvalid
	}

	return user, nil
}

// rotateRefreshToken выполняет ротацию refresh токена.
func (au *authService) rotateRefreshToken(ctx context.Context, user *User, oldJTI, fid string) (*LoginResult, error) {
	newJTI := uuid.NewString()

	req := cache.RotateRefreshRequest{
		OldJTI: oldJTI,
		NewJTI: newJTI,
		NewRecord: cache.RefreshRecord{
			FID:    fid,
			Status: "active",
			UserID: user.ID,
		},
		TTL: au.cfg.RefreshTokenTTL,
	}

	if err := au.cache.RotateRefresh(ctx, req); err != nil {
		return nil, apperr.ErrInternal.WithError(err)
	}

	newRefreshToken, err := au.generateRefreshToken(user, newJTI)
	if err != nil {
		return nil, fmt.Errorf("generate new refresh token: %w", err)
	}

	accessToken, err := au.generateAccessToken(user)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	return &LoginResult{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
	}, nil
}

// Refresh выполняет ротацию refresh токена.
func (au *authService) Refresh(ctx context.Context, refreshToken string) (*LoginResult, error) {
	claims, record, err := au.validateRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}

	if err := au.checkRefreshStatus(ctx, record); err != nil {
		return nil, err
	}

	user, err := au.getUserFromRefresh(ctx, claims.Subject)
	if err != nil {
		return nil, err
	}

	return au.rotateRefreshToken(ctx, user, claims.ID, record.FID)
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

func (au *authService) resendEmail(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		return err
	}

	emailHash := au.crp.Hash([]byte(email))
	user, err := au.repo.GetUserByEmailHash(ctx, emailHash)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil
		}
		return err
	}
	if user.EmailVerified {
		return nil
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		return fmt.Errorf("authService.resendEmail: parse user id: %w", err)
	}

	cred, err := au.repo.GetAuthCredByUserID(ctx, userID)
	if err != nil {
		return err
	}
	if cred == nil {
		return nil
	}

	emailDecrypted, err := au.crp.Decrypt(cred.EmailEncrypted)
	if err != nil {
		return fmt.Errorf("authService.resendEmail: %w", err)
	}

	// Генерируем новый токен
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

	go au.sendVerificationEmail(
		context.WithoutCancel(ctx),
		userID,
		string(emailDecrypted),
		tokenRaw,
	)

	return nil
}

// GenerateOAuthJWT generates a JWT for OAuth users.
// The access token is intentionally returned in the API response.
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
		"exp":   now.Add(au.cfg.AccessTokenTTL).Unix(),
		"iat":   now.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["typ"] = "access" // Required by the auth middleware
	tokenString, err := token.SignedString([]byte(au.cfg.JWTSecret))
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	return tokenString, nil
}

// Register creates a new user account.
func (au *authService) Register(ctx context.Context, req RegisterRequest) error {
	email := normalizeEmail(req.Email)
	if err := ValidateEmail(email); err != nil {
		return err
	}
	if err := ValidatePassword(req.Password); err != nil {
		return err
	}

	emailHash := au.crp.Hash([]byte(email))

	// Check if user already exists
	existingUser, err := au.repo.GetUserByEmailHash(ctx, emailHash)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return fmt.Errorf("check user exists: %w", err)
	}

	// Begin transaction
	tx, err := au.repo.beginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Error("tx rollback failed", "err", err)
		}
	}()

	txRepo := au.repo.withTx(tx)

	var userID uuid.UUID

	if existingUser != nil {
		// Если email уже подтвержден - конфликт
		if existingUser.EmailVerified {
			// Отправляем письмо о попытке регистрации
			go au.sendAccountExistsEmail(ctx, email)
			return apperr.ErrConflict.WithMessage("email already registered")
		}

		// Неподтвержденный пользователь - reclaim
		userID, err = uuid.Parse(existingUser.ID)
		if err != nil {
			return fmt.Errorf("parse existing user id: %w", err)
		}

		// Проверяем, есть ли у пользователя организация
		if existingUser.OrgID == nil {
			orgID, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("generate org id: %w", err)
			}
			if err := txRepo.CreateOrganization(ctx, orgID, "Personal organization"); err != nil {
				return fmt.Errorf("create organization: %w", err)
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET org_id = $1 WHERE id = $2`, orgID, userID); err != nil {
				return fmt.Errorf("update user org_id: %w", err)
			}
		}

		// Инвалидируем старые verify токены
		if err := txRepo.InvalidateAllVerifyTokens(ctx, userID); err != nil {
			return fmt.Errorf("invalidate old tokens: %w", err)
		}

		// Удаляем старые auth_cred
		if _, err := tx.Exec(ctx, `DELETE FROM auth_cred WHERE user_id = $1`, userID); err != nil {
			return fmt.Errorf("delete old auth_cred: %w", err)
		}

		// Хешируем новый пароль
		passwordHash, err := hashPassword(req.Password, au.cfg.BcryptCost)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}

		// Шифруем email
		emailEncrypted, err := au.crp.Encrypt([]byte(email))
		if err != nil {
			return fmt.Errorf("encrypt email: %w", err)
		}

		// Создаем новые auth_cred
		if err := txRepo.CreateAuthCred(ctx, CreateAuthCredParams{
			UserID:         userID,
			EmailHash:      emailHash,
			EmailEncrypted: emailEncrypted,
			PasswordHash:   passwordHash,
			Role:           "defectologist",
		}); err != nil {
			return fmt.Errorf("create auth_cred: %w", err)
		}

		// Сбрасываем email_verified
		if _, err := tx.Exec(ctx, `UPDATE users SET email_verified = false WHERE id = $1`, userID); err != nil {
			return fmt.Errorf("reset email_verified: %w", err)
		}

	} else {
		// Новый пользователь - создаем организацию
		orgID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate org id: %w", err)
		}

		if err := txRepo.CreateOrganization(ctx, orgID, "Personal organization"); err != nil {
			return fmt.Errorf("create organization: %w", err)
		}

		userID, err = uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate user id: %w", err)
		}

		passwordHash, err := hashPassword(req.Password, au.cfg.BcryptCost)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}

		emailEncrypted, err := au.crp.Encrypt([]byte(email))
		if err != nil {
			return fmt.Errorf("encrypt email: %w", err)
		}

		if err := txRepo.CreateOAuthUser(ctx, CreateUserParams{
			ID:             userID,
			OrganizationID: &orgID,
			Name:           email,
			EmailVerified:  false,
		}); err != nil {
			return fmt.Errorf("create user: %w", err)
		}

		if err := txRepo.CreateAuthCred(ctx, CreateAuthCredParams{
			UserID:         userID,
			EmailHash:      emailHash,
			EmailEncrypted: emailEncrypted,
			PasswordHash:   passwordHash,
			Role:           "defectologist",
		}); err != nil {
			return fmt.Errorf("create auth_cred: %w", err)
		}
	}

	// Создаем новый verify token
	tokenRaw := make([]byte, 32)
	if _, err := rand.Read(tokenRaw); err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	tokenHash := sha256.Sum256(tokenRaw)

	tokenID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate token id: %w", err)
	}

	if err := txRepo.rotateEmailTokens(
		ctx,
		tokenID,
		userID,
		tokenHash[:],
		time.Now().Add(au.cfg.VerifyEmailTokenTTL),
	); err != nil {
		return fmt.Errorf("create verify token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	go au.sendVerificationEmail(context.WithoutCancel(ctx), userID, email, tokenRaw)

	return nil
}

func (au *authService) sendVerificationEmail(ctx context.Context, userID uuid.UUID, email string, tokenRaw []byte) {
	if au.mailer == nil {
		slog.ErrorContext(ctx, "mailer is not configured", "user_id", userID)
		return
	}

	verifyURL := au.cfg.FrontendURL +
		"/verify-email?token=" +
		base64.RawURLEncoding.EncodeToString(tokenRaw)

	if err := au.mailer.Send(ctx, email, mailer.EmailVerify, mailer.EmailData{
		Token: verifyURL,
		Email: email,
	}); err != nil {
		slog.ErrorContext(ctx, "failed to send verification email",
			"user_id", userID,
			"error", err,
		)
	}
}

// sendAccountExistsEmail отправляет письмо о попытке регистрации на уже существующий email
func (au *authService) sendAccountExistsEmail(ctx context.Context, email string) {
	if au.mailer == nil {
		slog.ErrorContext(ctx, "mailer is not configured")
		return
	}

	if err := au.mailer.Send(ctx, email, mailer.AccountExists, mailer.EmailData{
		Email: email,
	}); err != nil {
		slog.ErrorContext(ctx, "failed to send account exists email",
			"email", email,
			"error", err,
		)
	}
}

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

// handleExistingIdentity processes existing identity.
func (au *authService) handleExistingIdentity(ctx context.Context, identity *UserIdentity, name string) (*User, *UserCred, error) {
	user, err := au.repo.GetUserByID(ctx, identity.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("get user by id: %w", err)
	}
	if user == nil {
		return nil, nil, fmt.Errorf("user not found for identity")
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

// createOAuthUser creates new user from OAuth provider.
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

// SendVerificationEmail sends email verification for OAuth user.
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

// Logout revokes the refresh token family. Идемпотентен: невалидный,
// истёкший или уже отозванный токен не считается ошибкой.
func (au *authService) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}

	_, record, err := au.validateRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil
	}

	if err := au.cache.RevokeFamily(ctx, record.FID); err != nil {
		return fmt.Errorf("authService.Logout: %w", err)
	}

	return nil
}
