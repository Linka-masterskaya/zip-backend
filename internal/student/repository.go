package student

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
		WITH created AS (
			INSERT INTO students (
				id, defectologist_id, email_encrypted, name, age, status,
				cards_shift, avatar_media_id
			)
			SELECT $1, u.id, $3, $4, $5, $6, $7, $8
			FROM users u
			WHERE u.id = $2 AND u.deleted_at IS NULL
			RETURNING `+studentColumns+`
		)
		SELECT `+studentColumnsWithAvatar+`
		FROM created s
		LEFT JOIN media_files m ON m.id = s.avatar_media_id`,
		uuid.New(), ownerID, emailEncrypted, input.Name, input.Age, input.Status,
		input.CardsShift, input.AvatarMediaID)
	result, err := scanStudent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("student create: %w", err)
	}
	return result, nil
}

// Get returns one non-deleted student owned by the defectologist.
func (r *Repository) Get(ctx context.Context, ownerID, studentID uuid.UUID) (*storedStudent, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+studentColumnsWithAvatar+`
		FROM students s
		LEFT JOIN media_files m ON m.id = s.avatar_media_id
		WHERE s.id = $2
		  AND s.defectologist_id = $1
		  AND s.deleted_at IS NULL`, ownerID, studentID)
	result, err := scanStudent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("student get: %w", err)
	}
	return result, nil
}

func (r *Repository) List(ctx context.Context, ownerID uuid.UUID, input ListInput) ([]storedStudent, int, error) {
	query, args := studentListQuery(ownerID, input)

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("student list begin: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			slog.WarnContext(ctx, "student list rollback", "err", rollbackErr)
		}
	}()

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("student list: %w", err)
	}
	defer rows.Close()

	var result []storedStudent
	for rows.Next() {
		item, err := scanStudent(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("student list scan: %w", err)
		}
		result = append(result, *item)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("student list rows: %w", err)
	}

	var totalCount int
	if err = tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM students
		WHERE defectologist_id = $1 AND deleted_at IS NULL
	`, ownerID).Scan(&totalCount); err != nil {
		return nil, 0, fmt.Errorf("student count: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("student list commit: %w", err)
	}
	return result, totalCount, nil
}

func studentListQuery(ownerID uuid.UUID, input ListInput) (string, []any) {
	query := `
		SELECT ` + studentColumnsWithAvatar + `
		FROM students s
		LEFT JOIN media_files m ON m.id = s.avatar_media_id
		WHERE s.defectologist_id = $1 AND s.deleted_at IS NULL
	`
	args := []any{ownerID}

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
		orderByClause = fmt.Sprintf("s.last_lesson_at %s NULLS LAST, s.id ASC", orderDir)
	case "name":
		orderByClause = fmt.Sprintf("lower(s.name) %s, s.id ASC", orderDir)
	case "age":
		orderByClause = fmt.Sprintf("s.age %s, s.id ASC", orderDir)
	case "status":
		orderByClause = fmt.Sprintf("s.status %s, s.id ASC", orderDir)
	default:
		orderByClause = fmt.Sprintf("lower(s.name) %s, s.id ASC", orderDir)
	}

	query += " ORDER BY " + orderByClause
	limitIndex := len(args) + 1
	offsetIndex := limitIndex + 1
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", limitIndex, offsetIndex)
	args = append(args, input.Limit, input.Offset)

	return query, args
}

func (r *Repository) Update(
	ctx context.Context,
	ownerID, studentID uuid.UUID,
	input storedUpdate,
) (*storedStudent, error) {
	row := r.pool.QueryRow(ctx, `
		WITH updated AS (
			UPDATE students
			SET email_encrypted = CASE WHEN $3 THEN $4 ELSE email_encrypted END,
			    email_verified = CASE WHEN $3 THEN false ELSE email_verified END,
			    name = COALESCE($5, name),
			    age = COALESCE($6, age),
			    status = COALESCE($7, status),
			    cards_shift = COALESCE($12, cards_shift),
			    last_lesson_at = CASE WHEN $8 THEN $9 ELSE last_lesson_at END,
			    avatar_media_id = CASE WHEN $10 THEN $11 ELSE avatar_media_id END,
			    updated_at = now()
			WHERE id = $2 AND defectologist_id = $1 AND deleted_at IS NULL
			RETURNING `+studentColumns+`
		)
		SELECT `+studentColumnsWithAvatar+`
		FROM updated s
		LEFT JOIN media_files m ON m.id = s.avatar_media_id`,
		ownerID, studentID, input.EmailSet, input.EmailEncrypted,
		input.Name, input.Age, input.Status,
		input.LastLessonSet, input.LastLessonAt,
		input.AvatarMediaIDSet, input.AvatarMediaID, input.CardsShift)
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

// Owned сообщает, есть ли у дефектолога такой ученик. Нужно до загрузки
// файла: иначе неверный id оставлял бы в банке медиа никому не нужную
// картинку.
func (r *Repository) Owned(ctx context.Context, ownerID, studentID uuid.UUID) (bool, error) {
	var owned bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM students
			WHERE id = $2 AND defectologist_id = $1 AND deleted_at IS NULL
		)`, ownerID, studentID).Scan(&owned)
	if err != nil {
		return false, fmt.Errorf("student owned check: %w", err)
	}
	return owned, nil
}

// AvatarMediaAccessible сообщает, существует ли медиа-файл и принадлежит ли он
// той же организации, что и владелец картотеки. Без проверки один
// дефектолог мог бы поставить ученику картинку из чужой организации.
func (r *Repository) AvatarMediaAccessible(
	ctx context.Context,
	ownerID, mediaID uuid.UUID,
) (bool, error) {
	var accessible bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM media_files m
			JOIN users u ON u.id = $1
			WHERE m.id = $2
			  AND m.org_id = u.org_id
			  AND u.org_id IS NOT NULL
			  AND u.deleted_at IS NULL
		)`, ownerID, mediaID).Scan(&accessible)
	if err != nil {
		return false, fmt.Errorf("student avatar media check: %w", err)
	}
	return accessible, nil
}

func scanStudent(row interface{ Scan(...any) error }) (*storedStudent, error) {
	var result storedStudent
	err := row.Scan(
		&result.ID, &result.EmailEncrypted, &result.EmailVerified,
		&result.Name, &result.Age, &result.Status, &result.CardsShift,
		&result.LastLessonAt,
		&result.AvatarMediaID, &result.AvatarKey,
		&result.CreatedAt, &result.UpdatedAt, &result.DeletedAt,
	)
	return &result, err
}

const studentColumns = `
	id, email_encrypted, email_verified, name, age, status, cards_shift,
	last_lesson_at, avatar_media_id, created_at, updated_at, deleted_at`

// studentColumnsWithAvatar добавляет ключ объекта аватара из media_files.
// Ключ нужен, чтобы сервис выписал presigned-ссылку; в самой таблице
// students его нет.
const studentColumnsWithAvatar = `
	s.id, s.email_encrypted, s.email_verified, s.name, s.age, s.status,
	s.cards_shift, s.last_lesson_at, s.avatar_media_id, m.minio_key,
	s.created_at, s.updated_at, s.deleted_at`

// collectAffectedMediaQuery собирает media_id, затронутые удалением
// ученика: из его паков, их адаптаций, версий и адаптаций чужих паков.
const collectAffectedMediaQuery = `
	SELECT DISTINCT mu.media_id FROM media_usages mu
	WHERE mu.source_id IN (
    SELECT id FROM packs WHERE folder_id = ANY($1)
    UNION ALL
    SELECT pa.id FROM pack_adaptations pa
        JOIN packs p ON pa.pack_id = p.id
        WHERE p.folder_id = ANY($1)
    UNION ALL
    SELECT pv.id FROM pack_versions pv
        JOIN packs p ON pv.pack_id = p.id
        WHERE p.folder_id = ANY($1)
    UNION ALL
    SELECT id FROM pack_adaptations WHERE student_id = $2
)`

// ForceDelete сносит ученика насовсем вместе с его папкой: soft delete
// оставляет карточку в базе, а здесь нужно именно полное удаление. Всё в
// одной транзакции — частично снесённая картотека хуже, чем не снесённая.
func (r *Repository) ForceDelete(ctx context.Context, ownerID, studentID uuid.UUID) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("student force delete begin: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			slog.WarnContext(ctx, "student force delete rollback", "err", rollbackErr)
		}
	}()

	var locked uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM students
		WHERE id = $2 AND defectologist_id = $1
		FOR UPDATE`, ownerID, studentID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("student force delete lock: %w", err)
	}

	folderIDs, maxDepth, err := studentFolderTree(ctx, tx, studentID)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, collectAffectedMediaQuery, folderIDs, studentID)
	if err != nil {
		return fmt.Errorf("student force delete collect media: %w", err)
	}
	mediaIDs, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return fmt.Errorf("student force delete collect media: %w", err)
	}
	if err = purgeStudentPacks(ctx, tx, studentID, folderIDs); err != nil {
		return err
	}
	if err = deleteOrphanedMedia(ctx, tx, mediaIDs); err != nil {
		return err
	}
	// Папки удаляются снизу вверх: folders.parent_id объявлен RESTRICT,
	// поэтому родителя нельзя снести раньше детей.
	for depth := maxDepth; depth >= 0; depth-- {
		if _, err = tx.Exec(ctx,
			`DELETE FROM folders WHERE id = ANY($1) AND depth = $2`,
			folderIDs, depth); err != nil {
			return fmt.Errorf("student force delete folders: %w", err)
		}
	}
	if _, err = tx.Exec(ctx,
		`DELETE FROM students WHERE id = $2 AND defectologist_id = $1`,
		ownerID, studentID); err != nil {
		return fmt.Errorf("student force delete: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("student force delete commit: %w", err)
	}
	return nil
}

// studentFolderTree возвращает папку ученика вместе со всеми вложенными.
// По владельцу здесь не фильтруем: право на удаление уже проверено локом
// строки ученика, а любая папка, ссылающаяся на него, обязана уйти — иначе
// folders.student_id с RESTRICT уронит транзакцию.
func studentFolderTree(
	ctx context.Context,
	tx pgx.Tx,
	studentID uuid.UUID,
) ([]uuid.UUID, int, error) {
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE tree AS (
			SELECT f.id, f.depth
			FROM folders f
			WHERE f.student_id = $1
			UNION ALL
			SELECT c.id, c.depth
			FROM folders c
			JOIN tree t ON c.parent_id = t.id
		)
		SELECT id, depth FROM tree`, studentID)
	if err != nil {
		return nil, 0, fmt.Errorf("student force delete folder tree: %w", err)
	}
	defer rows.Close()

	folderIDs := make([]uuid.UUID, 0)
	maxDepth := 0
	for rows.Next() {
		var (
			id    uuid.UUID
			depth int
		)
		if err = rows.Scan(&id, &depth); err != nil {
			return nil, 0, fmt.Errorf("student force delete folder scan: %w", err)
		}
		folderIDs = append(folderIDs, id)
		if depth > maxDepth {
			maxDepth = depth
		}
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("student force delete folder rows: %w", err)
	}
	return folderIDs, maxDepth, nil
}

// purgeStudentPacks удаляет наборы из папок ученика и следы использования
// медиа. media_usages не связаны с packs внешним ключом, поэтому строки
// нужно снимать руками — иначе файлы навсегда останутся «занятыми» и их
// не выйдет удалить из банка.
func purgeStudentPacks(
	ctx context.Context,
	tx pgx.Tx,
	studentID uuid.UUID,
	folderIDs []uuid.UUID,
) error {
	if len(folderIDs) > 0 {
		// Опубликованный набор уходит вместе с остальными: просили удалять
		// ученика «даже с папками», а отказ вернул бы ровно тот тупик, из-за
		// которого ручку и заводили. Публикация — это ссылка на библиотечную
		// папку в самой строке набора, поэтому отдельно снимать её не нужно.
		if _, err := tx.Exec(ctx, `
			DELETE FROM media_usages
			WHERE source_type = 'pack'
			  AND source_id IN (SELECT id FROM packs WHERE folder_id = ANY($1))`,
			folderIDs); err != nil {
			return fmt.Errorf("student force delete pack usages: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM media_usages
			WHERE source_type = 'pack_adaptation'
			  AND source_id IN (
				SELECT pa.id FROM pack_adaptations pa
				JOIN packs p ON p.id = pa.pack_id
				WHERE p.folder_id = ANY($1)
			  )`, folderIDs); err != nil {
			return fmt.Errorf("student force delete pack adaptation usages: %w", err)
		}
		// Версии наборов уходят каскадом вместе с наборами.
		if _, err := tx.Exec(ctx, `
			DELETE FROM media_usages
			WHERE source_type = 'pack_version'
			  AND source_id IN (
				SELECT pv.id FROM pack_versions pv
				JOIN packs p ON p.id = pv.pack_id
				WHERE p.folder_id = ANY($1)
			  )`, folderIDs); err != nil {
			return fmt.Errorf("student force delete pack version usages: %w", err)
		}
	}
	// Адаптации самого ученика уходят каскадом вместе с ним, включая те,
	// что сделаны из чужих наборов, — их следы тоже надо снять.
	if _, err := tx.Exec(ctx, `
		DELETE FROM media_usages
		WHERE source_type = 'pack_adaptation'
		  AND source_id IN (SELECT id FROM pack_adaptations WHERE student_id = $1)`,
		studentID); err != nil {
		return fmt.Errorf("student force delete adaptation usages: %w", err)
	}
	if len(folderIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM packs WHERE folder_id = ANY($1)`, folderIDs); err != nil {
		return fmt.Errorf("student force delete packs: %w", err)
	}
	return nil
}

const deleteOrphanedMediaQuery = `
WITH deleted AS (
	DELETE FROM media_files
	WHERE id = ANY($1)
		AND NOT EXISTS (
				SELECT 1 FROM media_usages WHERE media_id = media_files.id
		)
		AND NOT EXISTS (
			SELECT 1 FROM students WHERE avatar_media_id = media_files.id
		)
		AND NOT EXISTS (
			SELECT 1 FROM tts_jobs WHERE media_id = media_files.id
		)
	RETURNING org_id, size_bytes
)
UPDATE organizations o
SET storage_used_bytes = GREATEST(o.storage_used_bytes - d.total, 0)
FROM (SELECT org_id, SUM(size_bytes) AS total FROM deleted GROUP BY org_id) d
WHERE o.id = d.org_id`

// deleteOrphanedMedia удаляет записи media_files без ссылок в media_usages
// и возвращает квоту организации.
func deleteOrphanedMedia(ctx context.Context, tx pgx.Tx, mediaIDs []uuid.UUID) error {
	if len(mediaIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, deleteOrphanedMediaQuery, mediaIDs); err != nil {
		return fmt.Errorf("delete orphaned media: %w", err)
	}
	return nil
}
