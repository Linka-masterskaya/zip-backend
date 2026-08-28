package pack

import (
	"context"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/httpquery"
	"github.com/google/uuid"
)

type favoriteService interface {
	Favorite(context.Context, uuid.UUID) error
	Unfavorite(context.Context, uuid.UUID) error
	ListFavorites(context.Context, ListInput) (*ListPage, error)
}

type FavoriteHandler struct {
	service favoriteService
}

func NewFavoriteHandler(service favoriteService) *FavoriteHandler {
	return &FavoriteHandler{service: service}
}

func (h *FavoriteHandler) PutFavorite(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	if err = h.service.Favorite(r.Context(), packID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *FavoriteHandler) DeleteFavorite(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	if err = h.service.Unfavorite(r.Context(), packID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *FavoriteHandler) ListFavorites(w http.ResponseWriter, r *http.Request) error {
	limit, err := httpquery.Int(r, "limit")
	if err != nil {
		return err
	}
	offset, err := httpquery.Int(r, "offset")
	if err != nil {
		return err
	}
	result, err := h.service.ListFavorites(r.Context(), ListInput{Limit: limit, Offset: offset})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}
