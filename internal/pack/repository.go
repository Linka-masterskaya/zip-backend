package pack

import (
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
)

// Repository обеспечивает работу с хранилищем Redis для паков.
type Repository struct {
	redisClient *cache.Client
}

// NewRepository создает новый экземпляр Repository.
func NewRepository(redisClient *cache.Client) *Repository {
	return &Repository{
		redisClient: redisClient,
	}
}
