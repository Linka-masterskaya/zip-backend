package folder

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFolderTreeDepthCycleAccessAndDelete(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "owner")
	foreignID := seedFolderUser(t, pool, "foreign")
	service := NewService(NewRepository(pool))
	ctx := folderContext(ownerID)

	root, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Root",
	})
	require.NoError(t, err)
	assert.Zero(t, root.Depth)

	parent := root
	byDepth := []*Folder{root}
	for depth := 1; depth <= 4; depth++ {
		parent, err = service.Create(ctx, CreateInput{
			ParentID: &parent.ID, Section: SectionMy, Kind: KindFolder, Name: "Nested",
		})
		require.NoError(t, err)
		assert.Equal(t, depth, parent.Depth)
		byDepth = append(byDepth, parent)
	}
	_, err = service.Create(ctx, CreateInput{
		ParentID: &parent.ID, Section: SectionMy, Kind: KindFolder, Name: "Too deep",
	})
	assertStatus(t, err, apperr.ErrBadRequest.HTTPStatus)

	_, err = service.Move(ctx, root.ID, &parent.ID)
	assertStatus(t, err, apperr.ErrConflict.HTTPStatus)

	subtreeRoot, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Subtree",
	})
	require.NoError(t, err)
	_, err = service.Create(ctx, CreateInput{
		ParentID: &subtreeRoot.ID, Section: SectionMy, Kind: KindFolder, Name: "Subtree child",
	})
	require.NoError(t, err)
	_, err = service.Move(ctx, subtreeRoot.ID, &byDepth[3].ID)
	assertStatus(t, err, apperr.ErrBadRequest.HTTPStatus)

	foreignRoot, err := service.Create(folderContext(foreignID), CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Foreign",
	})
	require.NoError(t, err)
	_, err = service.Create(ctx, CreateInput{
		ParentID: &foreignRoot.ID, Section: SectionMy, Kind: KindFolder, Name: "Denied",
	})
	assertStatus(t, err, apperr.ErrBadRequest.HTTPStatus)

	err = service.Delete(ctx, root.ID)
	assertStatus(t, err, apperr.ErrConflict.HTTPStatus)
	err = service.Delete(folderContext(foreignID), root.ID)
	assertStatus(t, err, apperr.ErrNotFound.HTTPStatus)
}

func TestStudentFolderOwnershipAndMixedContents(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "owner")
	foreignID := seedFolderUser(t, pool, "foreign")
	studentID := seedFolderStudent(t, pool, ownerID)
	foreignStudentID := seedFolderStudent(t, pool, foreignID)
	service := NewService(NewRepository(pool))
	ctx := folderContext(ownerID)

	studentFolder, err := service.Create(ctx, CreateInput{
		Section: SectionStudents, Kind: KindStudent, StudentID: &studentID, Name: "Анна",
	})
	require.NoError(t, err)
	_, err = service.Create(ctx, CreateInput{
		Section: SectionStudents, Kind: KindStudent,
		StudentID: &foreignStudentID, Name: "Чужой",
	})
	assertStatus(t, err, apperr.ErrNotFound.HTTPStatus)

	child, err := service.Create(ctx, CreateInput{
		ParentID: &studentFolder.ID, Section: SectionStudents, Kind: KindFolder, Name: "Материалы",
	})
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `
		INSERT INTO packs (org_id, owner_id, folder_id, title, config)
		SELECT org_id, id, $2, 'Набор', '{}'::jsonb
		FROM users WHERE id = $1`, ownerID, studentFolder.ID)
	require.NoError(t, err)

	page, err := service.Contents(ctx, ContentsInput{
		Section: SectionStudents, ParentID: &studentFolder.ID,
	})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.Equal(t, "folder", page.Items[0].Type)
	assert.Equal(t, child.ID, page.Items[0].ID)
	assert.Equal(t, "pack", page.Items[1].Type)
}

func TestConcurrentChildCreateAndParentDeleteNeverCascadesData(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "owner")
	service := NewService(NewRepository(pool))
	ctx := folderContext(ownerID)
	root, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Root",
	})
	require.NoError(t, err)

	start := make(chan struct{})
	createResult := make(chan error, 1)
	deleteResult := make(chan error, 1)
	go func() {
		<-start
		_, createErr := service.Create(ctx, CreateInput{
			ParentID: &root.ID, Section: SectionMy, Kind: KindFolder, Name: "Child",
		})
		createResult <- createErr
	}()
	go func() {
		<-start
		deleteResult <- service.Delete(ctx, root.ID)
	}()
	close(start)
	createErr, deleteErr := <-createResult, <-deleteResult

	var rootCount, childCount, orphanCount int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM folders WHERE id = $1`, root.ID).Scan(&rootCount))
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM folders WHERE parent_id = $1`, root.ID).Scan(&childCount))
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM folders child
		LEFT JOIN folders parent ON parent.id = child.parent_id
		WHERE child.parent_id IS NOT NULL AND parent.id IS NULL`).Scan(&orphanCount))

	assert.Zero(t, orphanCount)
	if createErr == nil {
		assert.Error(t, deleteErr)
		assert.Equal(t, 1, rootCount)
		assert.Equal(t, 1, childCount)
	} else {
		require.NoError(t, deleteErr)
		assert.Zero(t, rootCount)
		assert.Zero(t, childCount)
	}
}

func TestLibraryAdminIsScopedToOrganization(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "owner")
	foreignHeadID := seedFolderUser(t, pool, "foreign head")
	service := NewService(NewRepository(pool))
	ownerCtx := folderContext(ownerID)
	foreignHeadCtx := folderContextWithRole(foreignHeadID, "head_defectologist")

	target, err := service.Create(ownerCtx, CreateInput{
		Section: SectionLibrary, Kind: KindFolder, Name: "Target",
	})
	require.NoError(t, err)
	destination, err := service.Create(ownerCtx, CreateInput{
		Section: SectionLibrary, Kind: KindFolder, Name: "Destination",
	})
	require.NoError(t, err)

	_, err = service.Rename(foreignHeadCtx, target.ID, "Cross-org rename")
	assertStatus(t, err, apperr.ErrNotFound.HTTPStatus)
	_, err = service.Move(foreignHeadCtx, target.ID, &destination.ID)
	assertStatus(t, err, apperr.ErrNotFound.HTTPStatus)
	err = service.Delete(foreignHeadCtx, target.ID)
	assertStatus(t, err, apperr.ErrNotFound.HTTPStatus)

	var ownerOrgID uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT org_id FROM users WHERE id = $1`, ownerID).Scan(&ownerOrgID))
	sameOrgHeadID := uuid.New()
	_, err = pool.Exec(t.Context(),
		`INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, 'Test User')`, sameOrgHeadID, ownerOrgID)
	require.NoError(t, err)
	sameOrgHeadCtx := folderContextWithRole(sameOrgHeadID, "head_defectologist")

	renamed, err := service.Rename(sameOrgHeadCtx, target.ID, "Same-org rename")
	require.NoError(t, err)
	assert.Equal(t, "Same-org rename", renamed.Name)
	moved, err := service.Move(sameOrgHeadCtx, target.ID, &destination.ID)
	require.NoError(t, err)
	require.NotNil(t, moved.ParentID)
	assert.Equal(t, destination.ID, *moved.ParentID)
}

func folderTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, cleanup := testutil.NewPostgres(t)
	t.Cleanup(cleanup)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, applyFolderMigrations(db))
	return pool
}

func applyFolderMigrations(db *sql.DB) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, "../../migrations")
}

func seedFolderUser(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	orgID, userID := uuid.New(), uuid.New()
	_, err := pool.Exec(
		context.Background(),
		`INSERT INTO organizations (id, name) VALUES ($1, $2)`,
		orgID, name,
	)
	require.NoError(t, err)
	_, err = pool.Exec(
		context.Background(),
		`INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, 'Test User')`,
		userID, orgID,
	)
	require.NoError(t, err)
	return userID
}

func seedFolderStudent(t *testing.T, pool *pgxpool.Pool, ownerID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO students (id, defectologist_id, email_encrypted, name, status)
		VALUES ($1, $2, $3, 'Student', 'active')`,
		id, ownerID, []byte("email"))
	require.NoError(t, err)
	return id
}

func folderContext(userID uuid.UUID) context.Context {
	return folderContextWithRole(userID, "defectologist")
}

func folderContextWithRole(userID uuid.UUID, role string) context.Context {
	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	return authctx.SetRoleToCtx(ctx, role)
}

func assertStatus(t *testing.T, err error, status int) {
	t.Helper()
	var appErr *apperr.AppError
	require.Error(t, err)
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, status, appErr.HTTPStatus)
}

func TestSectionRootContentsAreScopedSortedAndFolderOnly(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "root owner")
	foreignID := seedFolderUser(t, pool, "root foreign")
	service := NewService(NewRepository(pool))
	ctx := folderContext(ownerID)

	beta, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Бета",
	})
	require.NoError(t, err)
	alpha, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Альфа",
	})
	require.NoError(t, err)

	// Вложенная папка не должна попасть в корень.
	_, err = service.Create(ctx, CreateInput{
		ParentID: &alpha.ID, Section: SectionMy, Kind: KindFolder, Name: "Вложенная",
	})
	require.NoError(t, err)

	// Папка другого раздела тоже не должна попасть в корень "my".
	_, err = service.Create(ctx, CreateInput{
		Section: SectionLibrary, Kind: KindFolder, Name: "Библиотечная",
	})
	require.NoError(t, err)

	// И папка чужого владельца.
	_, err = service.Create(folderContext(foreignID), CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Чужая",
	})
	require.NoError(t, err)

	page, err := service.Contents(ctx, ContentsInput{Section: SectionMy})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.Equal(t, []string{"Альфа", "Бета"}, []string{page.Items[0].Name, page.Items[1].Name})
	assert.Equal(t, alpha.ID, page.Items[0].ID)
	assert.Equal(t, beta.ID, page.Items[1].ID)
	for _, item := range page.Items {
		assert.Equal(t, "folder", item.Type)
	}

	desc, err := service.Contents(ctx, ContentsInput{Section: SectionMy, Order: "desc"})
	require.NoError(t, err)
	require.Len(t, desc.Items, 2)
	assert.Equal(t, "Бета", desc.Items[0].Name)

	_, err = service.Contents(ctx, ContentsInput{})
	assertStatus(t, err, apperr.ErrBadRequest.HTTPStatus)
}

func TestContentsRejectsFolderFromAnotherSection(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "section mismatch")
	service := NewService(NewRepository(pool))
	ctx := folderContext(ownerID)

	libraryFolder, err := service.Create(ctx, CreateInput{
		Section: SectionLibrary, Kind: KindFolder, Name: "Общая",
	})
	require.NoError(t, err)

	_, err = service.Contents(ctx, ContentsInput{
		Section: SectionMy, ParentID: &libraryFolder.ID,
	})
	assertStatus(t, err, apperr.ErrNotFound.HTTPStatus)
}

func TestContentsTotalIgnoresPaginationAndVisibility(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "total owner")
	foreignID := seedFolderUser(t, pool, "total foreign")
	service := NewService(NewRepository(pool))
	ctx := folderContext(ownerID)

	for _, name := range []string{"Альфа", "Бета", "Гамма"} {
		_, err := service.Create(ctx, CreateInput{
			Section: SectionMy, Kind: KindFolder, Name: name,
		})
		require.NoError(t, err)
	}
	_, err := service.Create(folderContext(foreignID), CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Чужая",
	})
	require.NoError(t, err)

	first, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	assert.Equal(t, 3, first.Total)
	assert.Equal(t, 1, first.Limit)
	assert.Zero(t, first.Offset)

	second, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, Limit: 1, Offset: 2,
	})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	assert.Equal(t, 3, second.Total)

	pastEnd, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, Limit: 1, Offset: 100,
	})
	require.NoError(t, err)
	assert.Empty(t, pastEnd.Items)
	assert.Equal(t, 3, pastEnd.Total)
}

// TestContentsFilters: поиск и фильтры работают на общем списке папок и
// наборов. Возраст и сложность есть только у наборов, поэтому такие
// фильтры сами по себе отсекают папки.
func TestContentsFilters(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "owner")
	service := NewService(NewRepository(pool))
	ctx := folderContext(ownerID)

	root, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Мои наборы",
	})
	require.NoError(t, err)
	_, err = service.Create(ctx, CreateInput{
		ParentID: &root.ID, Section: SectionMy, Kind: KindFolder, Name: "Азбука папка",
	})
	require.NoError(t, err)
	_, err = service.Create(ctx, CreateInput{
		ParentID: &root.ID, Section: SectionMy, Kind: KindFolder, Name: "Счёт",
	})
	require.NoError(t, err)

	_, err = pool.Exec(context.Background(), `
		INSERT INTO packs (org_id, owner_id, folder_id, title, config, age_min, age_max, difficulty)
		SELECT org_id, id, $2, 'Азбука набор', '{}'::jsonb, 4, 6, 'easy'
		FROM users WHERE id = $1`, ownerID, root.ID)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `
		INSERT INTO packs (org_id, owner_id, folder_id, title, config, age_min, age_max, difficulty)
		SELECT org_id, id, $2, 'Счёт набор', '{}'::jsonb, 7, 9, 'hard'
		FROM users WHERE id = $1`, ownerID, root.ID)
	require.NoError(t, err)

	byQuery, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, ParentID: &root.ID, Query: "азбука",
	})
	require.NoError(t, err)
	require.Len(t, byQuery.Items, 2, "поиск идёт и по папкам, и по наборам")
	assert.Equal(t, 2, byQuery.Total)

	onlyPacks, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, ParentID: &root.ID, Type: "pack",
	})
	require.NoError(t, err)
	require.Len(t, onlyPacks.Items, 2)
	for _, item := range onlyPacks.Items {
		assert.Equal(t, "pack", item.Type)
	}

	onlyFolders, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, ParentID: &root.ID, Type: "folder",
	})
	require.NoError(t, err)
	require.Len(t, onlyFolders.Items, 2)

	age := 5
	byAge, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, ParentID: &root.ID, Age: &age,
	})
	require.NoError(t, err)
	require.Len(t, byAge.Items, 1, "у папок возраста нет, остаётся только набор")
	assert.Equal(t, "Азбука набор", byAge.Items[0].Name)

	byDifficulty, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, ParentID: &root.ID, Difficulty: "hard",
	})
	require.NoError(t, err)
	require.Len(t, byDifficulty.Items, 1)
	assert.Equal(t, "Счёт набор", byDifficulty.Items[0].Name)

	combined, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, ParentID: &root.ID, Query: "счёт", Difficulty: "easy",
	})
	require.NoError(t, err)
	assert.Empty(t, combined.Items)
	assert.Equal(t, 0, combined.Total)

	// В корне раздела фильтры тоже работают: там только папки.
	rootPacks, err := service.Contents(ctx, ContentsInput{Section: SectionMy, Type: "pack"})
	require.NoError(t, err)
	assert.Empty(t, rootPacks.Items)
}
