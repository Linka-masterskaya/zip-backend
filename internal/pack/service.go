package pack

import (
	"context"

	"github.com/Linka-masterskaya/zip-backend/internal/broker"
)

type Service struct {
	repo      *Repository
	publisher *broker.Publisher
}

func NewService(repo *Repository, publisher *broker.Publisher) *Service {
	return &Service{
		repo:      repo,
		publisher: publisher,
	}
}

// Create — заглушка (stub) для создания нового пака.
func (s *Service) Create(ctx context.Context, name string) (*Pack, error) {
	_ = ctx
	return &Pack{ID: "stub-id", Name: name}, nil
}

// Get — заглушка (stub) для получения пака по идентификатору.
func (s *Service) Get(ctx context.Context, id string) (*Pack, error) {
	_ = ctx
	return &Pack{ID: id, Name: "stub-name"}, nil
}

// List — заглушка (stub) для получения списка всех паков.
func (s *Service) List(ctx context.Context) ([]*Pack, error) {
	_ = ctx
	return []*Pack{}, nil
}
