package student

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("student not found")
	ErrHasFolder = errors.New("student has a folder")
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(
	ctx context.Context,
	ownerID uuid.UUID,
	emailEncrypted []byte,
	input CreateInput,
) (*storedStudent, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO students (
			id, defectologist_id, email_encrypted, name, age, status
		)
		SELECT $1, u.id, $3, $4, $5, $6
		FROM users u
		WHERE u.id = $2 AND u.deleted_at IS NULL
		RETURNING `+studentColumns,
		uuid.New(), ownerID, emailEncrypted, input.Name, input.Age, input.Status)
	result, err := scanStudent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("student create: %w", err)
	}
	return result, nil
}

func (r *Repository) List(ctx context.Context, ownerID uuid.UUID, input ListInput) ([]storedStudent, int, error) {
	var totalCount int
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM students
		WHERE defectologist_id = $1 AND deleted_at IS NULL
	`, ownerID).Scan(&totalCount); err != nil {
		return nil, 0, fmt.Errorf("student count: %w", err)
	}
	query := `
		SELECT ` + studentColumns + `
		FROM students
		WHERE defectologist_id = $1 AND deleted_at IS NULL
	`
	args := []any{ownerID}
	argIdx := 2

	sortBy := "name"
	if input.SortBy != "" {
		sortBy = input.SortBy
	}

	orderDir := "ASC"
	if strings.ToLower(input.Order) == "desc" {
		orderDir = "DESC"
	}

	var orderByClause string
	switch sortBy {
	case "last_lesson_at":
		orderByClause = fmt.Sprintf("last_lesson_at %s NULLS LAST, id ASC", orderDir)
	case "name":
		orderByClause = fmt.Sprintf("lower(name) %s, id ASC", orderDir)
	case "age":
		orderByClause = fmt.Sprintf("age %s, id ASC", orderDir)
	case "status":
		orderByClause = fmt.Sprintf("status %s, id ASC", orderDir)
	default:
		orderByClause = fmt.Sprintf("lower(name) %s, id ASC", orderDir)
	}

	query += " ORDER BY " + orderByClause

	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, input.Limit, input.Offset)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("student list: %w", err)
	}
	defer rows.Close()

	var result []storedStudent
	for rows.Next() {
		var item storedStudent
		if err := rows.Scan(
			&item.ID, &item.EmailEncrypted, &item.EmailVerified, &item.Name, &item.Age, &item.Status, &item.LastLessonAt,
			&item.CreatedAt, &item.UpdatedAt, &item.DeletedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("student list scan: %w", err)
		}
		result = append(result, item)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("student list rows: %w", err)
	}
	return result, totalCount, nil
}

func (r *Repository) Update(
	ctx context.Context,
	ownerID, studentID uuid.UUID,
	input storedUpdate,
) (*storedStudent, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE students
		SET email_encrypted = CASE WHEN $3 THEN $4 ELSE email_encrypted END,
		    email_verified = CASE WHEN $3 THEN false ELSE email_verified END,
		    name = COALESCE($5, name),
		    age = COALESCE($6, age),
		    status = COALESCE($7, status),
			last_lesson_at = CASE WHEN $8 THEN $9 ELSE last_lesson_at END,
		    updated_at = now()
		WHERE id = $2 AND defectologist_id = $1 AND deleted_at IS NULL
		RETURNING `+studentColumns,
		ownerID, studentID, input.EmailSet, input.EmailEncrypted,
		input.Name, input.Age, input.Status,
		input.LastLessonSet, input.LastLessonAt)
	result, err := scanStudent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("student update: %w", err)
	}
	return result, nil
}

func (r *Repository) Delete(ctx context.Context, ownerID, studentID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE students s
		SET deleted_at = now(), updated_at = now()
		WHERE s.id = $2
		  AND s.defectologist_id = $1
		  AND s.deleted_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM folders f WHERE f.student_id = s.id
		  )`, ownerID, studentID)
	if err != nil {
		return fmt.Errorf("student delete: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var hasFolder bool
	err = r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM students s
			JOIN folders f ON f.student_id = s.id
			WHERE s.id = $2 AND s.defectologist_id = $1 AND s.deleted_at IS NULL
		)`, ownerID, studentID).Scan(&hasFolder)
	if err != nil {
		return fmt.Errorf("student delete state: %w", err)
	}
	if hasFolder {
		return ErrHasFolder
	}
	return ErrNotFound
}

func scanStudent(row interface{ Scan(...any) error }) (*storedStudent, error) {
	var result storedStudent
	err := row.Scan(
		&result.ID, &result.EmailEncrypted, &result.EmailVerified,
		&result.Name, &result.Age, &result.Status, &result.LastLessonAt,
		&result.CreatedAt, &result.UpdatedAt, &result.DeletedAt,
	)
	return &result, err
}

const studentColumns = `
	id, email_encrypted, email_verified, name, age, status, last_lesson_at,
	created_at, updated_at, deleted_at`
