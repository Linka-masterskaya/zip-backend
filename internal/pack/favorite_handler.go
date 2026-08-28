package pack

import (
	"context"
	"net/http"

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
	limit, err := optionalQueryInt(r, "limit")
	if err != nil {
		return err
	}
	offset, err := optionalQueryInt(r, "offset")
	if err != nil {
		return err
	}
	result, err := h.service.ListFavorites(r.Context(), ListInput{Limit: limit, Offset: offset})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}
