package pack

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeFavoriteService struct {
	favoriteFn      func(context.Context, uuid.UUID) error
	unfavoriteFn    func(context.Context, uuid.UUID) error
	listFavoritesFn func(context.Context, ListInput) (*ListPage, error)
}

func (f *fakeFavoriteService) Favorite(ctx context.Context, packID uuid.UUID) error {
	if f.favoriteFn != nil {
		return f.favoriteFn(ctx, packID)
	}
	return nil
}

func (f *fakeFavoriteService) Unfavorite(ctx context.Context, packID uuid.UUID) error {
	if f.unfavoriteFn != nil {
		return f.unfavoriteFn(ctx, packID)
	}
	return nil
}

func (f *fakeFavoriteService) ListFavorites(ctx context.Context, input ListInput) (*ListPage, error) {
	if f.listFavoritesFn != nil {
		return f.listFavoritesFn(ctx, input)
	}
	return &ListPage{Items: []*ListItem{}}, nil
}

func TestFavoriteHandlerPutFavorite(t *testing.T) {
	packID := uuid.New()
	service := &fakeFavoriteService{}
	favoriteCalled := false
	service.favoriteFn = func(_ context.Context, gotPackID uuid.UUID) error {
		favoriteCalled = true
		assert.Equal(t, packID, gotPackID)
		return nil
	}
	handler := NewFavoriteHandler(service)

	rec := performPackRequest(
		t, handler.PutFavorite, http.MethodPut,
		"/api/v1/packs/"+packID.String()+"/favorite", nil, packID.String(),
	)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.True(t, favoriteCalled)
}

func TestFavoriteHandlerPutFavoriteNotFound(t *testing.T) {
	service := &fakeFavoriteService{}
	service.favoriteFn = func(context.Context, uuid.UUID) error {
		return apperr.ErrNotFound
	}
	handler := NewFavoriteHandler(service)
	packID := uuid.New()

	rec := performPackRequest(
		t, handler.PutFavorite, http.MethodPut,
		"/api/v1/packs/"+packID.String()+"/favorite", nil, packID.String(),
	)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestFavoriteHandlerDeleteFavorite(t *testing.T) {
	packID := uuid.New()
	service := &fakeFavoriteService{}
	unfavoriteCalled := false
	service.unfavoriteFn = func(_ context.Context, gotPackID uuid.UUID) error {
		unfavoriteCalled = true
		assert.Equal(t, packID, gotPackID)
		return nil
	}
	handler := NewFavoriteHandler(service)

	rec := performPackRequest(
		t, handler.DeleteFavorite, http.MethodDelete,
		"/api/v1/packs/"+packID.String()+"/favorite", nil, packID.String(),
	)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.True(t, unfavoriteCalled)
}

func TestFavoriteHandlerListFavorites(t *testing.T) {
	packID := uuid.New()
	service := &fakeFavoriteService{}
	service.listFavoritesFn = func(_ context.Context, input ListInput) (*ListPage, error) {
		assert.Equal(t, ListInput{Limit: 25, Offset: 10}, input)
		return &ListPage{Items: []*ListItem{{ID: packID, IsFavorite: true}}, Limit: 25, Offset: 10, Total: 42}, nil
	}
	handler := NewFavoriteHandler(service)

	rec := performPackRequest(
		t, handler.ListFavorites, http.MethodGet,
		"/api/v1/favorites/packs?limit=25&offset=10", nil, "",
	)

	assert.Equal(t, http.StatusOK, rec.Code)
	var result ListPage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(t, result.Items, 1)
	assert.Equal(t, packID, result.Items[0].ID)
	assert.True(t, result.Items[0].IsFavorite)
	assert.Equal(t, 25, result.Limit)
	assert.Equal(t, 10, result.Offset)
	assert.Equal(t, 42, result.Total)
}
