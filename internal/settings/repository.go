package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Get(ctx context.Context, userID uuid.UUID) (json.RawMessage, error) {
	var body []byte
	err := r.pool.QueryRow(ctx, `
		SELECT settings
		FROM user_settings
		WHERE user_id = $1
	`, userID).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return json.RawMessage(`{}`), nil
	}
	if err != nil {
		return nil, fmt.Errorf("settings.Get: %w", err)
	}
	return json.RawMessage(body), nil
}

func (r *Repository) Put(ctx context.Context, userID uuid.UUID, body json.RawMessage) (json.RawMessage, error) {
	var stored []byte
	err := r.pool.QueryRow(ctx, `
		INSERT INTO user_settings (user_id, settings)
		VALUES ($1, $2::jsonb)
		ON CONFLICT (user_id) DO UPDATE
		SET settings = EXCLUDED.settings,
		    updated_at = now()
		RETURNING settings
	`, userID, string(body)).Scan(&stored)
	if err != nil {
		return nil, fmt.Errorf("settings.Put: %w", err)
	}
	return json.RawMessage(stored), nil
}

func (r *Repository) ListTemplates(ctx context.Context, userID uuid.UUID) ([]Template, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, body, created_at, updated_at
		FROM user_setting_templates
		WHERE user_id = $1
		ORDER BY updated_at DESC, id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("settings.ListTemplates: %w", err)
	}
	defer rows.Close()

	templates := make([]Template, 0)
	for rows.Next() {
		var item Template
		var body []byte
		if err := rows.Scan(&item.ID, &item.Name, &body, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("settings.ListTemplates scan: %w", err)
		}
		item.Body = json.RawMessage(body)
		templates = append(templates, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("settings.ListTemplates rows: %w", err)
	}
	return templates, nil
}

func (r *Repository) CreateTemplate(ctx context.Context, userID uuid.UUID, name string, body json.RawMessage) (*Template, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings.CreateTemplate begin: %w", err)
	}
	defer rollbackSettingsTx(ctx, tx)

	// Serialize creates for one user so the quota cannot be bypassed by
	// concurrent requests racing between COUNT and INSERT.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, userID.String()); err != nil {
		return nil, fmt.Errorf("settings.CreateTemplate lock: %w", err)
	}

	var count int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM user_setting_templates
		WHERE user_id = $1
	`, userID).Scan(&count); err != nil {
		return nil, fmt.Errorf("settings.CreateTemplate count: %w", err)
	}
	if count >= MaxTemplatesPerUser {
		return nil, apperr.ErrConflict.WithMessage(fmt.Sprintf("template limit of %d reached", MaxTemplatesPerUser))
	}

	var item Template
	var storedBody []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO user_setting_templates (user_id, name, body)
		VALUES ($1, $2, $3::jsonb)
		RETURNING id, name, body, created_at, updated_at
	`, userID, name, string(body)).Scan(
		&item.ID, &item.Name, &storedBody, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "user_setting_templates_user_name_unique" {
			return nil, apperr.ErrConflict.WithMessage("template name already exists")
		}
		return nil, fmt.Errorf("settings.CreateTemplate: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("settings.CreateTemplate commit: %w", err)
	}
	item.Body = json.RawMessage(storedBody)
	return &item, nil
}

func (r *Repository) DeleteTemplate(ctx context.Context, userID, templateID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM user_setting_templates
		WHERE id = $1 AND user_id = $2
	`, templateID, userID)
	if err != nil {
		return fmt.Errorf("settings.DeleteTemplate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

func rollbackSettingsTx(ctx context.Context, tx pgx.Tx) {
	err := tx.Rollback(ctx)
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return
	}
}
