package picturebank

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const localSearchLimit = 100

type localPictureMetadata struct {
	ID        uuid.UUID
	Category  string
	Title     string
	MIMEType  string
	SizeBytes int64
	MinIOKey  string
	CreatedAt time.Time
}

type localRepository struct {
	pool *pgxpool.Pool
}

func newLocalRepository(pool *pgxpool.Pool) *localRepository {
	return &localRepository{pool: pool}
}

func (r *localRepository) Categories(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT category
		FROM picture_bank_images
		ORDER BY category
	`)
	if err != nil {
		return nil, fmt.Errorf("local picture bank categories: %w", err)
	}
	defer rows.Close()

	categories := make([]string, 0)
	for rows.Next() {
		var category string
		if err = rows.Scan(&category); err != nil {
			return nil, fmt.Errorf("local picture bank categories scan: %w", err)
		}
		categories = append(categories, category)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("local picture bank categories rows: %w", err)
	}
	return categories, nil
}

func (r *localRepository) Search(ctx context.Context, query string) ([]localPictureMetadata, error) {
	pattern := escapeLikePattern(query)
	rows, err := r.pool.Query(ctx, `
		SELECT id, category, title, mime_type, size_bytes, minio_key, created_at
		FROM picture_bank_images
		WHERE title ILIKE '%' || $1 || '%' ESCAPE E'\\'
		ORDER BY similarity(title, $2) DESC, lower(title), id
		LIMIT $3
	`, pattern, query, localSearchLimit)
	if err != nil {
		return nil, fmt.Errorf("local picture bank search: %w", err)
	}
	defer rows.Close()

	pictures := make([]localPictureMetadata, 0)
	for rows.Next() {
		var picture localPictureMetadata
		if err = scanLocalPicture(rows, &picture); err != nil {
			return nil, fmt.Errorf("local picture bank search scan: %w", err)
		}
		pictures = append(pictures, picture)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("local picture bank search rows: %w", err)
	}
	return pictures, nil
}

func (r *localRepository) Get(ctx context.Context, id uuid.UUID) (*localPictureMetadata, error) {
	var picture localPictureMetadata
	err := scanLocalPicture(r.pool.QueryRow(ctx, `
		SELECT id, category, title, mime_type, size_bytes, minio_key, created_at
		FROM picture_bank_images
		WHERE id = $1
	`, id), &picture)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPictureNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("local picture bank get: %w", err)
	}
	return &picture, nil
}

func (r *localRepository) Create(ctx context.Context, picture localPictureMetadata) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO picture_bank_images (
			id, category, title, mime_type, size_bytes, minio_key
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, picture.ID, picture.Category, picture.Title, picture.MIMEType, picture.SizeBytes, picture.MinIOKey)
	if err != nil {
		return fmt.Errorf("local picture bank create metadata: %w", err)
	}
	return nil
}

func (r *localRepository) Delete(ctx context.Context, id uuid.UUID) error {
	command, err := r.pool.Exec(ctx, `DELETE FROM picture_bank_images WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("local picture bank delete metadata: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrPictureNotFound
	}
	return nil
}

func (r *localRepository) PicturesByCategory(ctx context.Context, category string) ([]localPictureMetadata, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, category, title, mime_type, size_bytes, minio_key, created_at
		FROM picture_bank_images
		WHERE category = $1
		ORDER BY lower(title), id
		LIMIT 100
	`, category)
	if err != nil {
		return nil, fmt.Errorf("local picture bank by category: %w", err)
	}
	defer rows.Close()

	pictures := make([]localPictureMetadata, 0)
	for rows.Next() {
		var picture localPictureMetadata
		if err = scanLocalPicture(rows, &picture); err != nil {
			return nil, fmt.Errorf("local picture bank by category scan: %w", err)
		}
		pictures = append(pictures, picture)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("local picture bank by catogory rows: %w", err)
	}
	return pictures, nil
}

type localRowScanner interface {
	Scan(...any) error
}

func scanLocalPicture(row localRowScanner, picture *localPictureMetadata) error {
	return row.Scan(
		&picture.ID,
		&picture.Category,
		&picture.Title,
		&picture.MIMEType,
		&picture.SizeBytes,
		&picture.MinIOKey,
		&picture.CreatedAt,
	)
}

// escapeLikePattern preserves Source.Search literal substring semantics while
// still allowing PostgreSQL to use the trigram index for ILIKE.
func escapeLikePattern(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}
