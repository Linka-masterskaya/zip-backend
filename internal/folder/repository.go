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
		FROM users u
		WHERE f.id = $2
		  AND u.id = $1
		  AND u.org_id IS NOT NULL
		  AND u.deleted_at IS NULL
		  AND f.org_id = u.org_id
		  AND (f.owner_id = u.id OR ($4 AND f.section = 'library'))
		RETURNING `+qualifiedFolderColumns,
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

	orgID, err := activeUserOrg(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	current, err := lockFolder(ctx, tx, folderID)
	if err != nil || current.OrgID != orgID || (current.OwnerID != userID &&
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
		DELETE FROM folders f USING users u
		WHERE f.id = $2
		  AND u.id = $1
		  AND u.org_id IS NOT NULL
		  AND u.deleted_at IS NULL
		  AND f.org_id = u.org_id
		  AND (f.owner_id = u.id OR ($3 AND f.section = 'library'))
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
			SELECT 1
			FROM folders f
			JOIN users u ON u.id = $1
			WHERE f.id = $2
			  AND u.org_id IS NOT NULL
			  AND u.deleted_at IS NULL
			  AND f.org_id = u.org_id
			  AND (f.owner_id = u.id OR ($3 AND f.section = 'library'))
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
	input ContentsInput,
) (*ContentsPage, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("folder contents begin: %w", err)
	}
	defer rollback(ctx, tx)

	current, breadcrumbs, err := r.ensureParentVisible(ctx, tx, userID, input)
	if err != nil {
		return nil, err
	}

	query, args := contentsQuery(userID, input)
	rows, err := tx.Query(ctx, query, args...)
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

	countQuery, countArgs := contentsCountQuery(userID, input)
	var total int
	if err = tx.QueryRow(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("folder contents count: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("folder contents commit: %w", err)
	}

	return &ContentsPage{
		CurrentFolder: current,
		Breadcrumbs:   breadcrumbs,
		Items:         items,
		Limit:         input.Limit,
		Offset:        input.Offset,
		Total:         total,
	}, nil
}

// ensureParentVisible проверяет, что запрошенная папка существует и доступна,
// и в том же запросе строит цепочку предков от корня раздела до неё самой.
// Для корня раздела (ParentID == nil) проверять нечего — он не строка в
// таблице, поэтому текущей папки нет, а путь состоит только из самого раздела.
func (r *Repository) ensureParentVisible(
	ctx context.Context,
	tx pgx.Tx,
	userID uuid.UUID,
	input ContentsInput,
) (*CurrentFolder, []BreadCrumbs, error) {
	breadcrumbs := []BreadCrumbs{{Name: sectionLabel(input.Section)}}
	if input.ParentID == nil {
		return nil, breadcrumbs, nil
	}
	orgID, err := currentUserOrg(ctx, tx, userID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `
         WITH RECURSIVE ancestors AS (
		 SELECT id, name, parent_id, section, owner_id, depth
		 FROM folders
		 WHERE id = $1 AND org_id = $2
		 UNION ALL
	     SELECT f.id, f.name, f.parent_id, f.section, f.owner_id, f.depth
		 FROM folders f
		 JOIN ancestors a ON f.id = a.parent_id
		 WHERE f.depth < a.depth AND f.org_id = $2)
		 SELECT id, name, parent_id, section, owner_id
		 FROM ancestors
		 ORDER BY depth ASC`, *input.ParentID, orgID)
	if err != nil {
		return nil, nil, fmt.Errorf("folder ancestors: %w", err)
	}
	defer rows.Close()

	var current *CurrentFolder
	var found bool
	for rows.Next() {
		var id uuid.UUID
		var name, section string
		var parentID *uuid.UUID
		var ownerID uuid.UUID
		if err = rows.Scan(&id, &name, &parentID, &section, &ownerID); err != nil {
			return nil, nil, fmt.Errorf("folder ancestors scan: %w", err)
		}
		breadcrumbs = append(breadcrumbs, BreadCrumbs{ID: &id, Name: name})
		current = &CurrentFolder{ID: id, Name: name, ParentID: parentID}
		if id == *input.ParentID {
			found = true
			// Папка из другого раздела или чужая (кроме library) считается
			// отсутствующей: ответ не должен подтверждать, что она
			// существует где-то ещё.
			if section != input.Section || (section != SectionLibrary && ownerID != userID) {
				return nil, nil, ErrNotFound
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("folder ancestors rows: %w", err)
	}
	if !found {
		return nil, nil, ErrNotFound
	}
	return current, breadcrumbs, nil
}

func contentsQuery(userID uuid.UUID, input ContentsInput) (string, []any) {
	base, args := contentsBaseQuery(userID, input)

	orderColumn := "name"
	if input.Sort == "updated_at" {
		orderColumn = "updated_at"
	}

	direction := "ASC"
	if input.Order == "desc" {
		direction = "DESC"
	}

	limitIndex := len(args) + 1
	offsetIndex := len(args) + 2
	query := base +
		"\n\t\tORDER BY " + orderColumn + " " + direction + ", id" +
		fmt.Sprintf("\n\t\tLIMIT $%d OFFSET $%d", limitIndex, offsetIndex)

	args = append(args, input.Limit, input.Offset)
	return query, args
}

func contentsCountQuery(userID uuid.UUID, input ContentsInput) (string, []any) {
	base, args := contentsBaseQuery(userID, input)
	return "SELECT count(*) FROM (" + base + ") AS counted", args
}

func contentsBaseQuery(userID uuid.UUID, input ContentsInput) (string, []any) {
	if input.ParentID == nil {
		// Корень раздела содержит только папки: packs.folder_id объявлен
		// NOT NULL, то есть набор всегда лежит внутри какой-то папки.
		query := `
		WITH items AS (
			SELECT 'folder'::text AS type, f.id, f.name, f.kind,
			       f.student_id, false AS published, f.updated_at,
			       NULL::int AS age_min, NULL::int AS age_max,
			       NULL::text AS difficulty
			FROM folders f
			WHERE f.parent_id IS NULL
			  AND f.section = $2
			  AND ($2 = 'library' OR f.owner_id = $1)
		)
		SELECT type, id, name, kind, student_id, published, updated_at
		FROM items`
		args := []any{userID, input.Section}
		return appendContentsFilters(query, args, input)
	}

	packFolderColumn := "p.folder_id"
	packScope := "AND p.owner_id = $2"
	if input.Section == SectionLibrary {
		packFolderColumn = "p.library_folder_id"
		packScope = "AND p.published_at IS NOT NULL"
	}

	studentAssignments := ""
	if input.Section == SectionStudents {
		studentAssignments = `
			UNION ALL
			SELECT 'pack', p.id, p.title, NULL::text, NULL::uuid,
			       false, p.updated_at, p.age_min, p.age_max, p.difficulty
			FROM folders student_folder
			JOIN students s ON s.id = student_folder.student_id
			               AND s.deleted_at IS NULL
			JOIN pack_adaptations pa ON pa.student_id = student_folder.student_id
			JOIN packs p ON p.id = pa.pack_id
			WHERE student_folder.id = $1
			  AND student_folder.owner_id = $2
			  AND p.folder_id <> $1`
	}

	query := `
		WITH items AS (
			SELECT 'folder'::text AS type, f.id, f.name, f.kind,
			       f.student_id, false AS published, f.updated_at,
			       NULL::int AS age_min, NULL::int AS age_max,
			       NULL::text AS difficulty
			FROM folders f
			WHERE f.parent_id = $1
			  AND f.section = $3
			  AND ($3 = 'library' OR f.owner_id = $2)
			UNION ALL
			SELECT 'pack', p.id, p.title, NULL::text, NULL::uuid,
			       p.published_at IS NOT NULL, p.updated_at,
			       p.age_min, p.age_max, p.difficulty
			FROM packs p
			WHERE ` + packFolderColumn + ` = $1 ` + packScope + studentAssignments + `
		)
		SELECT type, id, name, kind, student_id, published, updated_at
		FROM items`
	args := []any{*input.ParentID, userID, input.Section}
	return appendContentsFilters(query, args, input)
}

// contentsFilters — общий хвост запроса: фильтры идут по одним и тем же
// плейсхолдерам, поэтому их номера фиксированы относительно args, а не
// считаются на лету. Возраст и сложность есть только у наборов, поэтому
// такие фильтры сами по себе отсекают папки — у них эти поля пустые.
func appendContentsFilters(query string, args []any, input ContentsInput) (string, []any) {
	first := len(args) + 1
	filters := fmt.Sprintf(`
		WHERE ($%d::text = '' OR name ILIKE '%%' || $%d::text || '%%')
		  AND ($%d::text = '' OR type = $%d::text)
		  AND ($%d::int IS NULL OR (age_min <= $%d::int AND $%d::int <= age_max))
		  AND ($%d::text = '' OR difficulty = $%d::text)`,
		first, first,
		first+1, first+1,
		first+2, first+2, first+2,
		first+3, first+3)

	args = append(args, input.Query, input.Type, input.Age, input.Difficulty)
	return query + filters, args
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

// currentUserOrg похож на activeUserOrg, но не требует FOR UPDATE, поэтому может выполняться
// внутри транзакции Contents, используется только для чтения.
func currentUserOrg(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (uuid.UUID, error) {
	var orgID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT org_id FROM users
		WHERE id = $1 AND org_id IS NOT NULL AND deleted_at IS NULL`, userID).Scan(&orgID)
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
