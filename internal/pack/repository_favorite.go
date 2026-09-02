package pack

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PutFavorite adds favorite bookmarks. Repeated calls are idempotent.
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

// DeleteFavorite deletes the pack bookmark for the user. Repeated calls are idempotent.
func (r *Repository) DeleteFavorite(ctx context.Context, userID, packID uuid.UUID) error {
	if _, err := r.pool.Exec(ctx, deleteFavoriteQuery, userID, packID); err != nil {
		return fmt.Errorf("pack repository delete favorite: %w", err)
	}
	return nil
}

// listFavorites returns a limited page with the selected packages currently available to the user.
// It should run in tx, like countFavorites (see ListWithTotal), so that both see the same snapshot.
func (r *Repository) listFavorites(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
	input ListInput,
) ([]*ListItem, error) {
	limit, offset := repositoryListBounds(input)
	rows, err := tx.Query(ctx, listFavoritePacksQuery, userID, limit, offset)
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

// countFavorites returns the total number of currently available favorite packages unchanged, broken down by page.
// The input data is accepted to verify the compliance of signatures using listFavorites and future filters;
// It should run in the same tx as listFavorites (see ListWithTotal) so that both see the same snapshot.
func (r *Repository) countFavorites(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
) (int, error) {
	var total int
	if err := tx.QueryRow(ctx, countFavoritePacksQuery, userID).Scan(&total); err != nil {
		return 0, fmt.Errorf("pack repository count favorites: %w", err)
	}

	return total, nil
}

// ListWithTotal returns a limited page of featured packages along with the total number,
// both are calculated within the same REPEATABLE READ, so they always display the same snapshot
// regardless of simultaneous featured/non-featured packages.
func (r *Repository) ListFavoritesWithTotal(
	ctx context.Context,
	userID uuid.UUID,
	input ListInput,
) ([]*ListItem, int, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("favorites list page begin: %w", err)
	}
	defer rollbackPackTx(ctx, tx)

	items, err := r.listFavorites(ctx, tx, userID, input)
	if err != nil {
		return nil, 0, err
	}
	total, err := r.countFavorites(ctx, tx, userID)
	if err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("favorite list page commit: %w", err)
	}
	return items, total, nil
}
