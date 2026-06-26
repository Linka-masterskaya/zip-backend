package pack

import (
	"github.com/Linka-masterskaya/zip-backend/internal/redis"
)

// Repository обеспечивает работу с хранилищем Redis для паков.
type Repository struct {
	redisClient *redis.Client
}

// NewRepository создает новый экземпляр Repository.
func NewRepository(redisClient *redis.Client) *Repository {
	return &Repository{
		redisClient: redisClient,
	}
}
