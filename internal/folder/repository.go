package folder

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound       = errors.New("folder not found")
	ErrParentInvalid  = errors.New("folder parent is invalid")
	ErrStudentInvalid = errors.New("student is invalid")
	ErrCycle          = errors.New("folder move creates a cycle")
	ErrDepth          = errors.New("folder depth limit exceeded")
	ErrNotEmpty       = errors.New("folder is not empty")
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(
	ctx context.Context,
	userID uuid.UUID,
	role string,
	input CreateInput,
) (*Folder, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("folder create begin: %w", err)
	}
	defer rollback(ctx, tx)

	orgID, err := activeUserOrg(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	depth := 0
	if input.ParentID != nil {
		parent, getErr := lockFolder(ctx, tx, *input.ParentID)
		if getErr != nil {
			return nil, ErrParentInvalid
		}
		if parent.OrgID != orgID || parent.Section != input.Section ||
			(!isAdmin(role) && parent.OwnerID != userID) {
			return nil, ErrParentInvalid
		}
		depth = parent.Depth + 1
		if depth > 4 {
			return nil, ErrDepth
		}
	}
	if input.Kind == KindStudent {
		var exists bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM students
				WHERE id = $1 AND defectologist_id = $2 AND deleted_at IS NULL
			)`, input.StudentID, userID).Scan(&exists)
		if err != nil {
			return nil, fmt.Errorf("folder validate student: %w", err)
		}
		if !exists {
			return nil, ErrStudentInvalid
		}
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO folders (
			org_id, owner_id, parent_id, section, kind, student_id, name, depth
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+folderColumns,
		orgID, userID, input.ParentID, input.Section, input.Kind, input.StudentID, input.Name, depth)
	result, err := scanFolder(row)
	if err != nil {
		return nil, fmt.Errorf("folder create: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("folder create commit: %w", err)
	}
	return result, nil
}

func (r *Repository) List(
	ctx context.Context,
	userID uuid.UUID,
	input ListInput,
) ([]Folder, error) {
	if input.ParentID != nil {
		var parentSection string
		var parentOwner uuid.UUID
		err := r.pool.QueryRow(ctx, `
			SELECT section, owner_id FROM folders WHERE id = $1`,
			input.ParentID).Scan(&parentSection, &parentOwner)
		if errors.Is(err, pgx.ErrNoRows) ||
			(err == nil && (parentSection != input.Section ||
				(input.Section != SectionLibrary && parentOwner != userID))) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("folder list parent: %w", err)
		}
	}

	args := []any{input.Section, input.ParentID, input.Limit, input.Offset}
	scope := ""
	if input.Section != SectionLibrary {
		scope = "AND f.owner_id = $5"
		args = append(args, userID)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT `+qualifiedFolderColumns+`
		FROM folders f
		WHERE f.section = $1
		  AND f.parent_id IS NOT DISTINCT FROM $2::uuid
		  `+scope+`
		ORDER BY lower(f.name), f.id
		LIMIT $3 OFFSET $4`, args...)
	if err != nil {
		return nil, fmt.Errorf("folder list: %w", err)
	}
	defer rows.Close()

	result := make([]Folder, 0)
	for rows.Next() {
		item, scanErr := scanFolder(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("folder list scan: %w", scanErr)
		}
		result = append(result, *item)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("folder list rows: %w", err)
	}
	return result, nil
}

func (r *Repository) Rename(
	ctx context.Context,
	userID uuid.UUID,
	role string,
	folderID uuid.UUID,
	name string,
) (*Folder, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE folders f
		SET name = $3, updated_at = now()
		WHERE f.id = $2
		  AND (f.owner_id = $1 OR ($4 AND f.section = 'library'))
		RETURNING `+folderColumns,
		userID, folderID, name, isAdmin(role))
	result, err := scanFolder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("folder rename: %w", err)
	}
	return result, nil
}

func (r *Repository) Move(
	ctx context.Context,
	userID uuid.UUID,
	role string,
	folderID uuid.UUID,
	parentID *uuid.UUID,
) (*Folder, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("folder move begin: %w", err)
	}
	defer rollback(ctx, tx)

	current, err := lockFolder(ctx, tx, folderID)
	if err != nil || (current.OwnerID != userID &&
		(!isAdmin(role) || current.Section != SectionLibrary)) {
		return nil, ErrNotFound
	}

	newDepth, err := moveDestinationDepth(
		ctx, tx, current, userID, role, folderID, parentID,
	)
	if err != nil {
		return nil, err
	}
	relativeMax, err := relativeSubtreeDepth(ctx, tx, folderID, current.Depth)
	if err != nil {
		return nil, err
	}
	if newDepth+relativeMax > 4 {
		return nil, ErrDepth
	}

	delta := newDepth - current.Depth
	row := tx.QueryRow(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id FROM folders WHERE id = $1
			UNION ALL
			SELECT f.id FROM folders f JOIN subtree s ON f.parent_id = s.id
		),
		updated AS (
			UPDATE folders f
			SET depth = f.depth + $3,
			    parent_id = CASE WHEN f.id = $1 THEN $2::uuid ELSE f.parent_id END,
			    updated_at = now()
			WHERE f.id IN (SELECT id FROM subtree)
			RETURNING f.*
		)
		SELECT `+folderColumns+` FROM updated WHERE id = $1`,
		folderID, parentID, delta)
	result, err := scanFolder(row)
	if err != nil {
		return nil, fmt.Errorf("folder move update: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("folder move commit: %w", err)
	}
	return result, nil
}

func moveDestinationDepth(
	ctx context.Context,
	tx pgx.Tx,
	current *Folder,
	userID uuid.UUID,
	role string,
	folderID uuid.UUID,
	parentID *uuid.UUID,
) (int, error) {
	if parentID == nil {
		return 0, nil
	}
	if *parentID == folderID {
		return 0, ErrCycle
	}
	parent, err := lockFolder(ctx, tx, *parentID)
	if err != nil || parent.OrgID != current.OrgID || parent.Section != current.Section ||
		(!isAdmin(role) && parent.OwnerID != userID) {
		return 0, ErrParentInvalid
	}
	var descendant bool
	err = tx.QueryRow(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id FROM folders WHERE id = $1
			UNION ALL
			SELECT f.id FROM folders f JOIN subtree s ON f.parent_id = s.id
		)
		SELECT EXISTS (SELECT 1 FROM subtree WHERE id = $2)`,
		folderID, *parentID).Scan(&descendant)
	if err != nil {
		return 0, fmt.Errorf("folder move cycle check: %w", err)
	}
	if descendant {
		return 0, ErrCycle
	}
	return parent.Depth + 1, nil
}

func relativeSubtreeDepth(
	ctx context.Context,
	tx pgx.Tx,
	folderID uuid.UUID,
	currentDepth int,
) (int, error) {
	var relativeMax int
	err := tx.QueryRow(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id, depth FROM folders WHERE id = $1
			UNION ALL
			SELECT f.id, f.depth FROM folders f JOIN subtree s ON f.parent_id = s.id
		)
		SELECT COALESCE(max(depth - $2), 0) FROM subtree`,
		folderID, currentDepth).Scan(&relativeMax)
	if err != nil {
		return 0, fmt.Errorf("folder move depth check: %w", err)
	}
	return relativeMax, nil
}

func (r *Repository) Delete(
	ctx context.Context,
	userID uuid.UUID,
	role string,
	folderID uuid.UUID,
) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM folders f
		WHERE f.id = $2
		  AND (f.owner_id = $1 OR ($3 AND f.section = 'library'))
		  AND NOT EXISTS (SELECT 1 FROM folders c WHERE c.parent_id = f.id)
		  AND NOT EXISTS (SELECT 1 FROM packs p WHERE p.folder_id = f.id)
		  AND NOT EXISTS (SELECT 1 FROM packs p WHERE p.library_folder_id = f.id)`,
		userID, folderID, isAdmin(role))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrNotEmpty
		}
		return fmt.Errorf("folder delete: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err = r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM folders
			WHERE id = $2 AND (owner_id = $1 OR ($3 AND section = 'library'))
		)`, userID, folderID, isAdmin(role)).Scan(&exists); err != nil {
		return fmt.Errorf("folder delete existence: %w", err)
	}
	if exists {
		return ErrNotEmpty
	}
	return ErrNotFound
}

func (r *Repository) Contents(
	ctx context.Context,
	userID uuid.UUID,
	folderID uuid.UUID,
	input ContentsInput,
) (*ContentsPage, error) {
	var section string
	var ownerID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT section, owner_id FROM folders WHERE id = $1`, folderID).Scan(&section, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && section != SectionLibrary && ownerID != userID) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("folder contents access: %w", err)
	}

	orderColumn := "name"
	if input.Sort == "updated_at" {
		orderColumn = "updated_at"
	}
	direction := "ASC"
	if input.Order == "desc" {
		direction = "DESC"
	}

	packFolderColumn := "p.folder_id"
	packScope := "AND p.owner_id = $2"
	if section == SectionLibrary {
		packFolderColumn = "p.library_folder_id"
		packScope = "AND p.published_at IS NOT NULL"
	}
	studentAssignments := ""
	if section == SectionStudents {
		studentAssignments = `
			UNION ALL
			SELECT 'pack', p.id, p.title, NULL::text, NULL::uuid,
			       false, p.updated_at
			FROM folders student_folder
			JOIN pack_adaptations pa ON pa.student_id = student_folder.student_id
			JOIN packs p ON p.id = pa.pack_id
			WHERE student_folder.id = $1
			  AND student_folder.owner_id = $2
			  AND p.folder_id <> $1`
	}
	query := `
		WITH items AS (
			SELECT 'folder'::text AS type, f.id, f.name, f.kind,
			       f.student_id, false AS published, f.updated_at
			FROM folders f
			WHERE f.parent_id = $1
			  AND f.section = $3
			  AND ($3 = 'library' OR f.owner_id = $2)
			UNION ALL
			SELECT 'pack', p.id, p.title, NULL::text, NULL::uuid,
			       p.published_at IS NOT NULL, p.updated_at
			FROM packs p
			WHERE ` + packFolderColumn + ` = $1 ` + packScope + studentAssignments + `
		)
		SELECT type, id, name, kind, student_id, published, updated_at
		FROM items
		ORDER BY ` + orderColumn + ` ` + direction + `, id
		LIMIT $4 OFFSET $5`
	rows, err := r.pool.Query(ctx, query, folderID, userID, section, input.Limit, input.Offset)
	if err != nil {
		return nil, fmt.Errorf("folder contents: %w", err)
	}
	defer rows.Close()

	items := make([]ContentItem, 0)
	for rows.Next() {
		var item ContentItem
		if err = rows.Scan(
			&item.Type, &item.ID, &item.Name, &item.Kind, &item.StudentID,
			&item.Published, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("folder contents scan: %w", err)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("folder contents rows: %w", err)
	}
	return &ContentsPage{Items: items, Limit: input.Limit, Offset: input.Offset}, nil
}

func activeUserOrg(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (uuid.UUID, error) {
	var orgID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT org_id FROM users
		WHERE id = $1 AND org_id IS NOT NULL AND deleted_at IS NULL
		FOR UPDATE`, userID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("folder user org: %w", err)
	}
	return orgID, nil
}

func lockFolder(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Folder, error) {
	item, err := scanFolder(tx.QueryRow(ctx, `
		SELECT `+qualifiedFolderColumns+`
		FROM folders f WHERE f.id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("folder lock: %w", err)
	}
	return item, nil
}

func scanFolder(row interface{ Scan(...any) error }) (*Folder, error) {
	var result Folder
	err := row.Scan(
		&result.ID, &result.OrgID, &result.OwnerID, &result.ParentID,
		&result.Section, &result.Kind, &result.StudentID, &result.Name,
		&result.Depth, &result.CreatedAt, &result.UpdatedAt,
	)
	return &result, err
}

func isAdmin(role string) bool {
	return strings.EqualFold(role, "admin") || strings.EqualFold(role, "head_defectologist")
}

func rollback(ctx context.Context, tx pgx.Tx) {
	err := tx.Rollback(ctx)
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return
	}
}

const folderColumns = `
	id, org_id, owner_id, parent_id, section, kind, student_id, name,
	depth, created_at, updated_at`

const qualifiedFolderColumns = `
	f.id, f.org_id, f.owner_id, f.parent_id, f.section, f.kind,
	f.student_id, f.name, f.depth, f.created_at, f.updated_at`
