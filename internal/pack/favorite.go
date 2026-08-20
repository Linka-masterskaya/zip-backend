package pack

import (
	"context"

	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/google/uuid"
)

type favoriteRepository interface {
	PutFavorite(context.Context, uuid.UUID, uuid.UUID) error
	DeleteFavorite(context.Context, uuid.UUID, uuid.UUID) error
	ListFavorites(context.Context, uuid.UUID, ListInput) ([]*ListItem, error)
}

// FavoriteService manages per-user pack bookmarks.
type FavoriteService struct {
	repo favoriteRepository
}

func NewFavoriteService(repo favoriteRepository) *FavoriteService {
	return &FavoriteService{repo: repo}
}

// Favorite bookmarks an accessible pack for the current user. Repeated calls are idempotent.
func (s *FavoriteService) Favorite(ctx context.Context, packID uuid.UUID) error {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return err
	}
	return packError(s.repo.PutFavorite(ctx, userID, packID))
}

// Unfavorite removes a pack bookmark for the current user. Repeated calls are idempotent.
func (s *FavoriteService) Unfavorite(ctx context.Context, packID uuid.UUID) error {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return err
	}
	return packError(s.repo.DeleteFavorite(ctx, userID, packID))
}

// ListFavorites returns a bounded page of the current user's favorited packs.
func (s *FavoriteService) ListFavorites(ctx context.Context, input ListInput) ([]*ListItem, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	input, err = validateListInput(input)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.ListFavorites(ctx, userID, input)
	return result, packError(err)
}
