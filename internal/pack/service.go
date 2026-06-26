package pack

import (
	"context"

	"github.com/Linka-masterskaya/zip-backend/internal/broker"
)

// Service описывает бизнес-логику работы с паками.
type Service struct {
	repo      *Repository
	publisher *broker.Publisher
}

// NewService создает новый экземпляр Service.
func NewService(repo *Repository, publisher *broker.Publisher) *Service {
	return &Service{
		repo:      repo,
		publisher: publisher,
	}
}

// Create создает новый пак и отправляет событие в брокер.
func (s *Service) Create(ctx context.Context, name string) (*Pack, error) {
	_ = ctx
	return &Pack{ID: "stub-id", Name: name}, nil
}

// Get возвращает пак по его идентификатору.
func (s *Service) Get(ctx context.Context, id string) (*Pack, error) {
	_ = ctx
	return &Pack{ID: id, Name: "stub-name"}, nil
}

// List возвращает список всех существующих паков.
func (s *Service) List(ctx context.Context) ([]*Pack, error) {
	_ = ctx
	return []*Pack{}, nil
}
