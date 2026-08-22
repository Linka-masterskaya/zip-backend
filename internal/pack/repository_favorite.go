package pack

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PutFavorite bookmarks. Repeated calls are idempotent.
func (r *Repository) PutFavorite(ctx context.Context, userID, packID uuid.UUID) error {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, putFavoriteQuery, userID, packID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPackNotFound
	}
	if err != nil {
		return fmt.Errorf("pack repository put favorite: %w", err)
	}
	return nil
}

// DeleteFavorite removes a pack bookmark for the user. Repeated calls are idempotent.
func (r *Repository) DeleteFavorite(ctx context.Context, userID, packID uuid.UUID) error {
	if _, err := r.pool.Exec(ctx, deleteFavoriteQuery, userID, packID); err != nil {
		return fmt.Errorf("pack repository delete favorite: %w", err)
	}
	return nil
}

// ListFavorites returns a bounded page of the user's currently accessible favorited packs.
func (r *Repository) ListFavorites(ctx context.Context, userID uuid.UUID, input ListInput) ([]*ListItem, error) {
	limit, offset := repositoryListBounds(input)
	rows, err := r.pool.Query(ctx, listFavoritePacksQuery, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("pack repository list favorites: %w", err)
	}
	defer rows.Close()

	packs := make([]*ListItem, 0)
	for rows.Next() {
		item, scanErr := scanListItem(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("pack repository list favorites scan: %w", scanErr)
		}
		packs = append(packs, item)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("pack repository list favorites rows: %w", err)
	}
	return packs, nil
}
