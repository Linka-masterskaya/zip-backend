package pack

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeFavoriteRepository struct {
	putFn    func(context.Context, uuid.UUID, uuid.UUID) error
	deleteFn func(context.Context, uuid.UUID, uuid.UUID) error
	listFn   func(context.Context, uuid.UUID, ListInput) ([]*ListItem, error)
}

func (f *fakeFavoriteRepository) PutFavorite(ctx context.Context, userID, packID uuid.UUID) error {
	if f.putFn != nil {
		return f.putFn(ctx, userID, packID)
	}
	return nil
}

func (f *fakeFavoriteRepository) DeleteFavorite(ctx context.Context, userID, packID uuid.UUID) error {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, userID, packID)
	}
	return nil
}

func (f *fakeFavoriteRepository) ListFavorites(
	ctx context.Context, userID uuid.UUID, input ListInput,
) ([]*ListItem, error) {
	if f.listFn != nil {
		return f.listFn(ctx, userID, input)
	}
	return []*ListItem{}, nil
}

func TestFavoriteServiceFavoriteDelegatesUserScope(t *testing.T) {
	userID := uuid.New()
	packID := uuid.New()
	repo := &fakeFavoriteRepository{}
	repo.putFn = func(_ context.Context, gotUserID, gotPackID uuid.UUID) error {
		assert.Equal(t, userID, gotUserID)
		assert.Equal(t, packID, gotPackID)
		return nil
	}

	err := NewFavoriteService(repo).Favorite(packContext(userID), packID)

	require.NoError(t, err)
}

func TestFavoriteServiceFavoriteMapsNotFound(t *testing.T) {
	repo := &fakeFavoriteRepository{}
	repo.putFn = func(context.Context, uuid.UUID, uuid.UUID) error {
		return ErrPackNotFound
	}

	err := NewFavoriteService(repo).Favorite(packContext(uuid.New()), uuid.New())

	assertAppErrorStatus(t, err, http.StatusNotFound)
}

func TestFavoriteServiceUnfavoriteDelegatesUserScope(t *testing.T) {
	userID := uuid.New()
	packID := uuid.New()
	repo := &fakeFavoriteRepository{}
	repo.deleteFn = func(_ context.Context, gotUserID, gotPackID uuid.UUID) error {
		assert.Equal(t, userID, gotUserID)
		assert.Equal(t, packID, gotPackID)
		return nil
	}

	err := NewFavoriteService(repo).Unfavorite(packContext(userID), packID)

	require.NoError(t, err)
}

func TestFavoriteServiceListFavoritesRejectsInvalidPagination(t *testing.T) {
	repo := &fakeFavoriteRepository{}

	_, err := NewFavoriteService(repo).ListFavorites(packContext(uuid.New()), ListInput{Limit: 101})

	assertAppErrorStatus(t, err, http.StatusBadRequest)
}

func TestFavoriteServiceListFavoritesAppliesDefaultLimit(t *testing.T) {
	userID := uuid.New()
	packID := uuid.New()
	repo := &fakeFavoriteRepository{}
	repo.listFn = func(_ context.Context, gotUserID uuid.UUID, input ListInput) ([]*ListItem, error) {
		assert.Equal(t, userID, gotUserID)
		assert.Equal(t, ListInput{Limit: 50}, input)
		return []*ListItem{{ID: packID, IsFavorite: true}}, nil
	}

	result, err := NewFavoriteService(repo).ListFavorites(packContext(userID), ListInput{})

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, packID, result[0].ID)
	assert.True(t, result[0].IsFavorite)
}
