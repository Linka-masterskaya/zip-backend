package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrEmailNotVerified = errors.New("email not verified")

type Service struct {
	repo  *Repository
	cache *cache.Client
	cfg   *ServiceConfig
}

type ServiceConfig struct {
	JWTSecret                string
	AccessTokenTTL           time.Duration
	RefreshTokenTTL          time.Duration
	RequireEmailVerification bool
}

type LoginResult struct {
	AccessToken  string
	RefreshToken string
}

func NewService(repo *Repository, cache *cache.Client, cfg *ServiceConfig) *Service {
	return &Service{
		repo:  repo,
		cache: cache,
		cfg:   cfg,
	}
}

func (s *Service) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	user, err := s.repo.GetUserByEmail(ctx, email)
	if errors.Is(err, ErrUserNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email error: %w", err)
	}
	if user.PasswordHash == nil {
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
	err = s.cache.StoreRefresh(ctx, jti, rec, s.cfg.RefreshTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("store refresh token: %w", err)
	}
	return &LoginResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}
