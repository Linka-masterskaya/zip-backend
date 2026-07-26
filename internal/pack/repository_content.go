package pack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) SaveConfig(
	ctx context.Context,
	userID, packID uuid.UUID,
	config json.RawMessage,
	mediaIDs []uuid.UUID,
) (*Pack, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pack config begin: %w", err)
	}
	defer rollbackPackTx(ctx, tx)

	var orgID uuid.UUID
	err = tx.QueryRow(ctx, lockPackForUpdateQuery, userID, packID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack config lock: %w", err)
	}
	if len(mediaIDs) > 0 {
		var count int
		err = tx.QueryRow(ctx, countPackMediaQuery, orgID, mediaIDs).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("pack config validate media: %w", err)
		}
		if count != len(mediaIDs) {
			return nil, ErrMediaNotAllowed
		}
	}
	if _, err = tx.Exec(ctx, deletePackMediaUsagesQuery, packID); err != nil {
		return nil, fmt.Errorf("pack config clear media usages: %w", err)
	}
	if len(mediaIDs) > 0 {
		if _, err = tx.Exec(ctx, insertPackMediaUsagesQuery, mediaIDs, packID); err != nil {
			return nil, fmt.Errorf("pack config insert media usages: %w", err)
		}
	}
	result, err := scanPack(tx.QueryRow(ctx, savePackConfigQuery, userID, packID, config))
	if err != nil {
		return nil, fmt.Errorf("pack config update: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pack config commit: %w", err)
	}
	return result, nil
}

func (r *Repository) ArchiveData(
	ctx context.Context,
	userID, packID uuid.UUID,
) (*Pack, []*media.File, error) {
	packData, err := r.Get(ctx, userID, packID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := r.pool.Query(ctx, archiveMediaQuery, packID)
	if err != nil {
		return nil, nil, fmt.Errorf("pack archive media: %w", err)
	}
	defer rows.Close()
	files := make([]*media.File, 0)
	for rows.Next() {
		var file media.File
		if err = rows.Scan(
			&file.ID, &file.OrgID, &file.UploaderID, &file.SHA256,
			&file.MIMEType, &file.SizeBytes, &file.MinIOKey, &file.CreatedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("pack archive scan media: %w", err)
		}
		files = append(files, &file)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("pack archive media rows: %w", err)
	}
	return packData, files, nil
}
