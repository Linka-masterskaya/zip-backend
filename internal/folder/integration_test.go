package folder

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
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
	assert.Nil(t, page.Items[1].Age)
	assert.Nil(t, page.Items[1].Difficulty)
}

func TestStudentAssignmentsExcludeArchivedStudents(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "owner")
	studentID := seedFolderStudent(t, pool, ownerID)
	service := NewService(NewRepository(pool))
	ctx := folderContext(ownerID)

	studentFolder, err := service.Create(ctx, CreateInput{
		Section: SectionStudents, Kind: KindStudent, StudentID: &studentID, Name: "Анна",
	})
	require.NoError(t, err)
	sourceFolder, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Материалы",
	})
	require.NoError(t, err)

	var packID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO packs (org_id, owner_id, folder_id, title, config)
		SELECT org_id, id, $2, 'Назначенный набор', '{}'::jsonb
		FROM users WHERE id = $1
		RETURNING id`, ownerID, sourceFolder.ID).Scan(&packID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO pack_adaptations (pack_id, student_id, config, created_by)
		VALUES ($1, $2, '{}'::jsonb, $3)`, packID, studentID, ownerID)
	require.NoError(t, err)

	active, err := service.Contents(ctx, ContentsInput{
		Section: SectionStudents, ParentID: &studentFolder.ID,
	})
	require.NoError(t, err)
	require.Len(t, active.Items, 1)
	assert.Equal(t, packID, active.Items[0].ID)
	assert.Equal(t, 1, active.Total)

	_, err = pool.Exec(ctx, `UPDATE students SET deleted_at = now() WHERE id = $1`, studentID)
	require.NoError(t, err)

	archived, err := service.Contents(ctx, ContentsInput{
		Section: SectionStudents, ParentID: &studentFolder.ID,
	})
	require.NoError(t, err)
	assert.Empty(t, archived.Items)
	assert.Zero(t, archived.Total)
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

func TestContentsReadsItemsAndTotalFromOneSnapshot(t *testing.T) {
	pool := folderTestDB(t)
	writerRepo := NewRepository(pool)
	ownerID := seedFolderUser(t, pool, "contents snapshot owner")

	_, err := writerRepo.Create(t.Context(), ownerID, "defectologist", CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "first",
	})
	require.NoError(t, err)

	gate := testutil.NewQueryGate()
	readerPoolConfig := pool.Config()
	readerPoolConfig.ConnConfig.Tracer = gate
	readerPool, err := pgxpool.NewWithConfig(t.Context(), readerPoolConfig)
	require.NoError(t, err)
	defer readerPool.Close()
	readerRepo := NewRepository(readerPool)

	type contentsResult struct {
		page *ContentsPage
		err  error
	}
	resultCh := make(chan contentsResult, 1)
	go func() {
		page, contentsErr := readerRepo.Contents(t.Context(), ownerID, ContentsInput{
			Section: SectionMy, Limit: 50,
		})
		resultCh <- contentsResult{page: page, err: contentsErr}
	}()
	defer gate.Release()
	gate.Wait(t, 5*time.Second)

	_, err = writerRepo.Create(t.Context(), ownerID, "defectologist", CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "second",
	})
	require.NoError(t, err)
	gate.Release()

	select {
	case result := <-resultCh:
		require.NoError(t, result.err)
		require.NotNil(t, result.page)
		require.NotEmpty(t, result.page.Items)
		assert.Equal(t, len(result.page.Items), result.page.Total)
	case <-time.After(5 * time.Second):
		t.Fatal("Contents did not return after the second query was released")
	}
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
		INSERT INTO packs (org_id, owner_id, folder_id, title, config, age, difficulty)
		SELECT org_id, id, $2, 'Азбука набор', '{}'::jsonb, 5, 'easy'
		FROM users WHERE id = $1`, ownerID, root.ID)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `
		INSERT INTO packs (org_id, owner_id, folder_id, title, config, age, difficulty)
		SELECT org_id, id, $2, 'Счёт набор', '{}'::jsonb, 8, 'hard'
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
	require.NotNil(t, byAge.Items[0].Age)
	assert.Equal(t, 5, *byAge.Items[0].Age)
	require.NotNil(t, byAge.Items[0].Difficulty)
	assert.Equal(t, "easy", *byAge.Items[0].Difficulty)

	ageFrom, ageTo := 6, 8
	byRange, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, ParentID: &root.ID, AgeFrom: &ageFrom, AgeTo: &ageTo,
	})
	require.NoError(t, err)
	require.Len(t, byRange.Items, 1)
	assert.Equal(t, "Счёт набор", byRange.Items[0].Name)
	assert.Equal(t, 1, byRange.Total)

	fromOnly, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, ParentID: &root.ID, AgeFrom: &ageFrom,
	})
	require.NoError(t, err)
	require.Len(t, fromOnly.Items, 1)
	assert.Equal(t, "Счёт набор", fromOnly.Items[0].Name)
	toOnly := 5
	upToAge, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, ParentID: &root.ID, AgeTo: &toOnly,
	})
	require.NoError(t, err)
	require.Len(t, upToAge.Items, 1)
	assert.Equal(t, "Азбука набор", upToAge.Items[0].Name)

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

func TestContentsBreadcrumbsForFourLevelDepth(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "breadcrumb owner")
	service := NewService(NewRepository(pool))
	ctx := folderContext(ownerID)

	names := []string{"Root", "Level1", "Level2", "Level3", "Level4"}
	chain := make([]*Folder, 0, len(names))
	var parentID *uuid.UUID
	for _, name := range names {
		folder, err := service.Create(ctx, CreateInput{
			ParentID: parentID, Section: SectionMy, Kind: KindFolder, Name: name,
		})
		require.NoError(t, err)
		chain = append(chain, folder)
		parentID = &folder.ID
	}
	require.Equal(t, 4, chain[4].Depth)

	page, err := service.Contents(ctx, ContentsInput{
		Section: SectionMy, ParentID: &chain[4].ID,
	})
	require.NoError(t, err)

	require.NotNil(t, page.CurrentFolder)
	assert.Equal(t, chain[4].ID, page.CurrentFolder.ID)
	assert.Equal(t, "Level4", page.CurrentFolder.Name)
	require.NotNil(t, page.CurrentFolder.ParentID)
	assert.Equal(t, chain[3].ID, *page.CurrentFolder.ParentID)

	require.Len(t, page.Breadcrumbs, len(names)+1)
	assert.Nil(t, page.Breadcrumbs[0].ID)
	assert.Equal(t, sectionLabel(SectionMy), page.Breadcrumbs[0].Name)
	for i, name := range names {
		crumb := page.Breadcrumbs[i+1]
		require.NotNil(t, crumb.ID)
		assert.Equal(t, chain[i].ID, *crumb.ID)
		assert.Equal(t, name, crumb.Name)
	}
}

func TestContentsRootHasSectionOnlyBreadcrumbAndNoCurrentFolder(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "root breadcrumb owner")
	service := NewService(NewRepository(pool))
	ctx := folderContext(ownerID)

	page, err := service.Contents(ctx, ContentsInput{Section: SectionMy})
	require.NoError(t, err)

	assert.Nil(t, page.CurrentFolder)
	require.Len(t, page.Breadcrumbs, 1)
	assert.Nil(t, page.Breadcrumbs[0].ID)
	assert.Equal(t, sectionLabel(SectionMy), page.Breadcrumbs[0].Name)
}

func TestContentsRejectsParentFromAnotherOrganization(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "cross-org owner")
	foreignID := seedFolderUser(t, pool, "cross-org foreign")
	service := NewService(NewRepository(pool))

	foreignFolder, err := service.Create(folderContext(foreignID), CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Foreign root",
	})
	require.NoError(t, err)
	_, err = service.Contents(folderContext(ownerID), ContentsInput{
		Section: SectionMy, ParentID: &foreignFolder.ID,
	})
	assertStatus(t, err, apperr.ErrNotFound.HTTPStatus)
}

func TestContentsLibraryBreadcrumbsScopedToOrganization(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "library breadcrumb owner")
	foreignID := seedFolderUser(t, pool, "library breadcrumb foreign")
	service := NewService(NewRepository(pool))

	libraryRoot, err := service.Create(folderContext(ownerID), CreateInput{
		Section: SectionLibrary, Kind: KindFolder, Name: "Shared",
	})
	require.NoError(t, err)
	child, err := service.Create(folderContext(ownerID), CreateInput{
		ParentID: &libraryRoot.ID, Section: SectionLibrary, Kind: KindFolder, Name: "Child",
	})
	require.NoError(t, err)

	var ownerOrgID uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT org_id FROM users WHERE id = $1`, ownerID).Scan(&ownerOrgID))
	sameOrgUserID := uuid.New()
	_, err = pool.Exec(t.Context(),
		`INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, 'Test User')`,
		sameOrgUserID, ownerOrgID)
	require.NoError(t, err)

	// Библиотека — общая на организацию: другой пользователь той же
	// организации должен видеть путь до чужой папки библиотеки.
	page, err := service.Contents(folderContext(sameOrgUserID), ContentsInput{
		Section: SectionLibrary, ParentID: &child.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, page.CurrentFolder)
	assert.Equal(t, child.ID, page.CurrentFolder.ID)
	require.Len(t, page.Breadcrumbs, 3)
	assert.Equal(t, libraryRoot.ID, *page.Breadcrumbs[1].ID)
	assert.Equal(t, child.ID, *page.Breadcrumbs[2].ID)

	// Но не пользователь чужой организации.
	_, err = service.Contents(folderContext(foreignID), ContentsInput{
		Section: SectionLibrary, ParentID: &child.ID,
	})
	assertStatus(t, err, apperr.ErrNotFound.HTTPStatus)
}
