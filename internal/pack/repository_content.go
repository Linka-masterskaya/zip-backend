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

func (r *Repository) Assign(
	ctx context.Context,
	userID, packID uuid.UUID,
	studentIDs []uuid.UUID,
) ([]Adaptation, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pack assignment begin: %w", err)
	}
	defer rollbackPackTx(ctx, tx)

	var orgID uuid.UUID
	var config json.RawMessage
	err = tx.QueryRow(ctx, lockPackConfigQuery, userID, packID).Scan(&orgID, &config)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack assignment lock: %w", err)
	}
	var count int
	if err = tx.QueryRow(ctx, countOwnedStudentsQuery, userID, studentIDs).Scan(&count); err != nil {
		return nil, fmt.Errorf("pack assignment validate students: %w", err)
	}
	if count != len(studentIDs) {
		return nil, ErrStudentNotAllowed
	}
	result := make([]Adaptation, 0, len(studentIDs))
	for _, studentID := range studentIDs {
		item, upsertErr := scanAdaptation(
			tx.QueryRow(ctx, upsertAdaptationQuery, packID, studentID, config, userID),
		)
		if upsertErr != nil {
			return nil, fmt.Errorf("pack assignment upsert: %w", upsertErr)
		}
		if _, upsertErr = tx.Exec(ctx, replaceAdaptationUsagesQuery, packID, item.ID); upsertErr != nil {
			return nil, fmt.Errorf("pack assignment media usages: %w", upsertErr)
		}
		result = append(result, *item)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pack assignment commit: %w", err)
	}
	return result, nil
}

func (r *Repository) Unassign(
	ctx context.Context,
	userID, packID, studentID uuid.UUID,
) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("pack unassign begin: %w", err)
	}
	defer rollbackPackTx(ctx, tx)
	var adaptationID uuid.UUID
	err = tx.QueryRow(ctx, lockAdaptationQuery, userID, packID, studentID).Scan(&adaptationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPackNotFound
	}
	if err != nil {
		return fmt.Errorf("pack unassign lock: %w", err)
	}
	if _, err = tx.Exec(ctx, deleteAdaptationUsagesQuery, adaptationID); err != nil {
		return fmt.Errorf("pack unassign media usages: %w", err)
	}
	if _, err = tx.Exec(ctx, deleteAdaptationQuery, adaptationID); err != nil {
		return fmt.Errorf("pack unassign delete: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("pack unassign commit: %w", err)
	}
	return nil
}

func scanAdaptation(row rowScanner) (*Adaptation, error) {
	var result Adaptation
	err := row.Scan(
		&result.ID, &result.PackID, &result.StudentID, &result.Config,
		&result.CreatedBy, &result.CreatedAt, &result.UpdatedAt,
	)
	return &result, err
}
