package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrEmailNotVerified = errors.New("email not verified")

var dummyPasswordHash = []byte("$2a$10$UlCQgLZoLjUzrtYRUUlkPeh/m5L2pl9aYzDTUaZAD3R4Pd8ONSof6")

type userRepository interface {
	GetUserByEmailHash(ctx context.Context, emailHash []byte) (*User, error)
}

type refreshStore interface {
	StoreRefresh(ctx context.Context, jti string, rec cache.RefreshRecord, ttl time.Duration) error
}

type emailHasher interface {
	Hash(data []byte) []byte
}

type Service struct {
	repo   userRepository
	cache  refreshStore
	cfg    *ServiceConfig
	crypto emailHasher
}

type ServiceConfig struct {
	JWTSecret                string
	AccessTokenTTL           time.Duration
	RefreshTokenTTL          time.Duration
	RequireEmailVerification bool
	CookieSecure             bool
}

type LoginResult struct {
	AccessToken  string
	RefreshToken string
}

func NewService(repo userRepository, cache refreshStore, cfg *ServiceConfig, crypto emailHasher) *Service {
	return &Service{
		repo:   repo,
		cache:  cache,
		cfg:    cfg,
		crypto: crypto,
	}
}

func (s *Service) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	emailHash := s.crypto.Hash([]byte(email))

	user, err := s.repo.GetUserByEmailHash(ctx, emailHash)
	if errors.Is(err, ErrUserNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email hash: %w", err)
	}

	if user.PasswordHash == nil {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	if s.cfg.RequireEmailVerification && !user.EmailVerified {
		return nil, ErrEmailNotVerified
	}

	accessToken, err := s.generateAccessToken(user)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	jti := uuid.NewString()
	fid := uuid.NewString()

	refreshToken, err := s.generateRefreshToken(user, jti)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	rec := cache.RefreshRecord{
		FID:    fid,
		Status: "active",
	}

	if err := s.cache.StoreRefresh(ctx, jti, rec, s.cfg.RefreshTokenTTL); err != nil {
		return nil, fmt.Errorf("store refresh token: %w", err)
	}

	return &LoginResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}
