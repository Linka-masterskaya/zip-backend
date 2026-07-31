package pack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateVersion stores an immutable snapshot of the current pack configuration.
func (r *Repository) CreateVersion(
	ctx context.Context,
	userID, packID uuid.UUID,
) (*Version, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pack version begin: %w", err)
	}
	defer rollbackPackTx(ctx, tx)

	_, config, err := lockPackConfig(ctx, tx, userID, packID)
	if err != nil {
		return nil, err
	}
	version, err := insertVersion(ctx, tx, packID, userID, config)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pack version commit: %w", err)
	}
	return version, nil
}

// ListVersions returns immutable snapshots newest first.
func (r *Repository) ListVersions(
	ctx context.Context,
	userID, packID uuid.UUID,
	input ListInput,
) ([]*VersionSummary, error) {
	limit, offset := repositoryListBounds(input)
	rows, err := r.pool.Query(ctx, listVersionsQuery, userID, packID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("pack versions list: %w", err)
	}
	defer rows.Close()

	versions := make([]*VersionSummary, 0)
	for rows.Next() {
		version, scanErr := scanVersionSummary(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("pack versions scan: %w", scanErr)
		}
		versions = append(versions, version)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("pack versions rows: %w", err)
	}
	if len(versions) == 0 {
		var exists bool
		if err = r.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM packs p
				JOIN users u ON u.id = $1
				WHERE p.id = $2
				  AND p.owner_id = u.id
				  AND p.org_id = u.org_id
				  AND u.deleted_at IS NULL
			)`, userID, packID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("pack versions access: %w", err)
		}
		if !exists {
			return nil, ErrPackNotFound
		}
	}
	return versions, nil
}

// GetVersion returns one full immutable snapshot.
func (r *Repository) GetVersion(
	ctx context.Context,
	userID, packID uuid.UUID,
	versionNumber int,
) (*Version, error) {
	version, err := scanVersion(
		r.pool.QueryRow(ctx, getAccessibleVersionQuery, userID, packID, versionNumber),
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVersionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack version get: %w", err)
	}
	return version, nil
}

// RestoreVersion checkpoints the current config and atomically restores a snapshot.
func (r *Repository) RestoreVersion(
	ctx context.Context,
	userID, packID uuid.UUID,
	versionNumber int,
) (*RestoreResult, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pack version restore begin: %w", err)
	}
	defer rollbackPackTx(ctx, tx)

	_, currentConfig, err := lockPackConfig(ctx, tx, userID, packID)
	if err != nil {
		return nil, err
	}
	target, err := scanVersion(tx.QueryRow(ctx, getVersionQuery, packID, versionNumber))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVersionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack version restore target: %w", err)
	}
	backup, err := insertVersion(ctx, tx, packID, userID, currentConfig)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, deletePackMediaUsagesQuery, packID); err != nil {
		return nil, fmt.Errorf("pack version restore clear media usages: %w", err)
	}
	if _, err = tx.Exec(ctx, replacePackUsagesFromVersionQuery, packID, target.ID); err != nil {
		return nil, fmt.Errorf("pack version restore media usages: %w", err)
	}
	restored, err := scanPack(tx.QueryRow(ctx, savePackConfigQuery, userID, packID, target.Config))
	if err != nil {
		return nil, fmt.Errorf("pack version restore config: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pack version restore commit: %w", err)
	}
	return &RestoreResult{
		Pack: restored, RestoredFromVersion: versionNumber, BackupVersion: backup,
	}, nil
}

func lockPackConfig(
	ctx context.Context,
	tx pgx.Tx,
	userID, packID uuid.UUID,
) (uuid.UUID, json.RawMessage, error) {
	var orgID uuid.UUID
	var config json.RawMessage
	err := tx.QueryRow(ctx, lockPackConfigQuery, userID, packID).Scan(&orgID, &config)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil, ErrPackNotFound
	}
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("pack version lock: %w", err)
	}
	return orgID, config, nil
}

func insertVersion(
	ctx context.Context,
	tx pgx.Tx,
	packID, userID uuid.UUID,
	config json.RawMessage,
) (*Version, error) {
	version, err := scanVersion(tx.QueryRow(ctx, createVersionQuery, packID, config, userID))
	if err != nil {
		return nil, fmt.Errorf("pack version insert: %w", err)
	}
	if _, err = tx.Exec(ctx, insertVersionMediaUsagesQuery, packID, version.ID); err != nil {
		return nil, fmt.Errorf("pack version media usages: %w", err)
	}
	return version, nil
}

func scanVersion(row rowScanner) (*Version, error) {
	var result Version
	err := row.Scan(
		&result.ID, &result.PackID, &result.Version,
		&result.Config, &result.CreatedBy, &result.CreatedAt,
	)
	return &result, err
}

func scanVersionSummary(row rowScanner) (*VersionSummary, error) {
	var result VersionSummary
	err := row.Scan(
		&result.ID, &result.PackID, &result.Version,
		&result.CreatedBy, &result.CreatedAt,
	)
	return &result, err
}
