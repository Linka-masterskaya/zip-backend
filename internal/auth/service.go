package auth

import (
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
)

// ServiceConfig содержит настройки для JWT и верификации.
type ServiceConfig struct {
	JWTSecret                string
	AccessTokenTTL           time.Duration
	RefreshTokenTTL          time.Duration
	RequireEmailVerification bool
}

// Service описывает бизнес-логику аутентификации.
type Service interface {

}

type service struct {
	repo   Repository
	cache  *cache.Client
	config *ServiceConfig
}

// NewService создает экземпляр сервиса.
func NewService(repo Repository, cacheClient *cache.Client, cfg *ServiceConfig) Service {
	return &service{
		repo:   repo,
		cache:  cacheClient,
		config: cfg,
	}
}