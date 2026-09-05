package pack

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryCRUDPreservesConfigAndClearsMetadata(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, userID, folderID := seedPackOwner(t, pool, "owner org")
	secondFolderID := seedPackFolder(t, pool, userID)
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	created, err := repo.Create(context.Background(), userID, CreateInput{
		Title: "Pack", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	assert.Equal(t, folderID, created.FolderID)
	assert.JSONEq(t, string(config), string(created.Config))

	age := 5
	difficulty := "medium"
	notes := "notes"
	goals := []string{"speech", "attention"}
	title := "Updated pack"
	updated, err := repo.Update(context.Background(), userID, created.ID, UpdateInput{
		Title: &title,
		FilterMetadata: &FilterMetadataPatch{
			Age:        NullablePatch[int]{Set: true, Value: &age},
			Difficulty: NullablePatch[string]{Set: true, Value: &difficulty},
			Goals:      &goals,
		},
		Notes: NullablePatch[string]{Set: true, Value: &notes},
	})
	require.NoError(t, err)
	assert.Equal(t, title, updated.Title)
	assert.Equal(t, goals, updated.Goals)
	assert.JSONEq(t, string(config), string(updated.Config), "PATCH must not change config")

	cleared, err := repo.Update(context.Background(), userID, created.ID, UpdateInput{
		FilterMetadata: &FilterMetadataPatch{
			Age:        NullablePatch[int]{Set: true},
			Difficulty: NullablePatch[string]{Set: true},
		},
		Notes: NullablePatch[string]{Set: true},
	})
	require.NoError(t, err)
	assert.Nil(t, cleared.Age)
	assert.Nil(t, cleared.Difficulty)
	assert.Empty(t, cleared.Notes)
	assert.JSONEq(t, string(config), string(cleared.Config))

	listed, _, err := repo.ListWithTotal(context.Background(), userID, ListInput{Limit: 50})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, created.ID, listed[0].ID)

	moved, err := repo.Move(context.Background(), userID, created.ID, secondFolderID)
	require.NoError(t, err)
	assert.Equal(t, secondFolderID, moved.FolderID)

	fetched, err := repo.Get(context.Background(), userID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, secondFolderID, fetched.FolderID)
	require.NoError(t, repo.Delete(context.Background(), userID, created.ID))
	_, err = repo.Get(context.Background(), userID, created.ID)
	assert.ErrorIs(t, err, ErrPackNotFound)
}

func TestRepositoryEnforcesUserAndFolderAccess(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, ownerID, ownerFolderID := seedPackOwner(t, pool, "owner org")
	_, foreignUserID, foreignFolderID := seedPackOwner(t, pool, "foreign org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := repo.Create(context.Background(), ownerID, CreateInput{
		Title: "Private", FolderID: ownerFolderID, Config: config,
	})
	require.NoError(t, err)

	_, err = repo.Create(context.Background(), ownerID, CreateInput{
		Title: "Wrong folder", FolderID: foreignFolderID, Config: config,
	})
	assert.ErrorIs(t, err, ErrFolderNotAllowed)
	_, err = repo.Get(context.Background(), foreignUserID, created.ID)
	assert.ErrorIs(t, err, ErrPackNotFound)
	listed, _, err := repo.ListWithTotal(context.Background(), ownerID, ListInput{Limit: 50})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, created.ID, listed[0].ID)
	_, err = repo.Update(context.Background(), foreignUserID, created.ID, UpdateInput{Title: stringPtr("foreign")})
	assert.ErrorIs(t, err, ErrPackNotFound)
	_, err = repo.Update(context.Background(), ownerID, created.ID, UpdateInput{FolderID: &foreignFolderID})
	assert.ErrorIs(t, err, ErrFolderNotAllowed)
	_, err = repo.Move(context.Background(), foreignUserID, created.ID, foreignFolderID)
	assert.ErrorIs(t, err, ErrPackNotFound)
	_, err = repo.Move(context.Background(), ownerID, created.ID, foreignFolderID)
	assert.ErrorIs(t, err, ErrFolderNotAllowed)
	assert.ErrorIs(t, repo.Delete(context.Background(), foreignUserID, created.ID), ErrPackNotFound)
}

func TestRepositoryListWithTotalReturnsItemsAndTotal(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, userID, folderID := seedPackOwner(t, pool, "pagination org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	created := make([]*Pack, 0, 3)
	for _, title := range []string{"first", "second", "third"} {
		item, err := repo.Create(context.Background(), userID, CreateInput{
			Title: title, FolderID: folderID, Config: config,
		})
		require.NoError(t, err)
		created = append(created, item)
	}
	for index, item := range created {
		_, err := pool.Exec(context.Background(), `
			UPDATE packs SET updated_at = to_timestamp($2) WHERE id = $1`, item.ID, index+1)
		require.NoError(t, err)
	}

	t.Run("page", func(t *testing.T) {
		items, total, err := repo.ListWithTotal(
			t.Context(), userID, ListInput{Limit: 1, Offset: 1},
		)

		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, created[1].ID, items[0].ID)
		assert.Equal(t, 3, total)
	})

	t.Run("offset past end", func(t *testing.T) {
		items, total, err := repo.ListWithTotal(
			t.Context(), userID, ListInput{Limit: 2, Offset: 100},
		)

		require.NoError(t, err)
		assert.Empty(t, items)
		assert.Equal(t, 3, total)
	})
}

func TestRepositoryListWithTotalFallbackReadsTotalFromSameSnapshot(t *testing.T) {
	pool := newPackTestDB(t)
	writerRepo := NewRepository(pool)
	_, userID, folderID := seedPackOwner(t, pool, "pagination snapshot org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	_, err := writerRepo.Create(t.Context(), userID, CreateInput{
		Title: "first", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)

	gate := testutil.NewQueryGate()
	readerPoolConfig := pool.Config()
	readerPoolConfig.ConnConfig.Tracer = gate
	readerPool, err := pgxpool.NewWithConfig(t.Context(), readerPoolConfig)
	require.NoError(t, err)
	defer readerPool.Close()
	readerRepo := NewRepository(readerPool)

	type listPageResult struct {
		items []*ListItem
		total int
		err   error
	}
	resultCh := make(chan listPageResult, 1)
	go func() {
		items, total, listErr := readerRepo.ListWithTotal(
			t.Context(), userID, ListInput{Limit: 50, Offset: 100},
		)
		resultCh <- listPageResult{items: items, total: total, err: listErr}
	}()
	defer gate.Release()
	gate.Wait(t, 5*time.Second)

	_, err = writerRepo.Create(t.Context(), userID, CreateInput{
		Title: "second", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	gate.Release()

	select {
	case result := <-resultCh:
		require.NoError(t, result.err)
		assert.Empty(t, result.items)
		assert.Equal(t, 1, result.total)
	case <-time.After(5 * time.Second):
		t.Fatal("ListWithTotal fallback did not return after the count query was released")
	}
}

func TestRepositoryListSearchesAndFiltersAccessiblePacks(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	orgID, userID, myFolderID := seedPackOwner(t, pool, "search org")
	studentFolderID := seedPackSectionFolder(t, pool, userID, "students")
	colleagueID, colleagueFolderID := seedPackUserInOrg(t, pool, orgID, "my")
	libraryFolderID := seedPackLibraryFolder(t, pool, colleagueID)
	_, foreignID, foreignFolderID := seedPackOwner(t, pool, "foreign search org")
	foreignLibraryID := seedPackLibraryFolder(t, pool, foreignID)
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	ownPack := createFilteredPack(t, repo, userID, myFolderID, "Speech Easy", 5, "easy", config)
	studentPack := createFilteredPack(t, repo, userID, studentFolderID, "Reading Hard", 7, "hard", config)
	privateColleague := createFilteredPack(t, repo, colleagueID, colleagueFolderID, "Speech Private", 5, "easy", config)
	publishedColleague := createFilteredPack(t, repo, colleagueID, colleagueFolderID, "SPEECH Medium", 5, "medium", config)
	_, err := repo.Publish(t.Context(), colleagueID, publishedColleague.ID, libraryFolderID, false)
	require.NoError(t, err)
	foreignPack := createFilteredPack(t, repo, foreignID, foreignFolderID, "Speech Foreign", 5, "easy", config)
	_, err = repo.Publish(t.Context(), foreignID, foreignPack.ID, foreignLibraryID, false)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `INSERT INTO favorite_packs (user_id, pack_id) VALUES ($1, $2)`, userID, ownPack.ID)
	require.NoError(t, err)

	age := 5
	listed, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{Query: "sPeEcH", Age: &age, Limit: 50})
	require.NoError(t, err)
	require.Len(t, listed, 2)
	items := listItemsByID(listed)
	require.Contains(t, items, ownPack.ID)
	require.Contains(t, items, publishedColleague.ID)
	assert.True(t, items[ownPack.ID].IsFavorite)
	assert.Equal(t, myFolderID, items[ownPack.ID].FolderID)
	assert.Equal(t, "my", items[ownPack.ID].Section)
	assert.False(t, items[publishedColleague.ID].IsFavorite)
	assert.Equal(t, libraryFolderID, items[publishedColleague.ID].FolderID)
	assert.Equal(t, "library", items[publishedColleague.ID].Section)
	assert.NotContains(t, items, privateColleague.ID)
	assert.NotContains(t, items, foreignPack.ID)

	easy, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{Difficulty: "easy", Limit: 50})
	require.NoError(t, err)
	require.Len(t, easy, 1)
	assert.Equal(t, ownPack.ID, easy[0].ID)
	medium, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{Difficulty: "medium", Limit: 50})
	require.NoError(t, err)
	require.Len(t, medium, 1)
	assert.Equal(t, publishedColleague.ID, medium[0].ID)
	hard, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{Difficulty: "hard", Limit: 50})
	require.NoError(t, err)
	require.Len(t, hard, 1)
	assert.Equal(t, studentPack.ID, hard[0].ID)

	nonMatchingAge := 4
	notMatched, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{
		Query: "Speech Easy", Age: &nonMatchingAge, Limit: 50,
	})
	require.NoError(t, err)
	assert.Empty(t, notMatched)

	ageFrom, ageTo := 6, 7
	inRange, inRangeTotal, err := repo.ListWithTotal(t.Context(), userID, ListInput{
		AgeFrom: &ageFrom, AgeTo: &ageTo, Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, inRange, 1)
	assert.Equal(t, studentPack.ID, inRange[0].ID)
	assert.Equal(t, 1, inRangeTotal)

	fromOnly, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{AgeFrom: &ageFrom, Limit: 50})
	require.NoError(t, err)
	require.Len(t, fromOnly, 1)
	assert.Equal(t, studentPack.ID, fromOnly[0].ID)
	toOnly := 5
	upToAge, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{AgeTo: &toOnly, Limit: 50})
	require.NoError(t, err)
	require.Len(t, upToAge, 2)

	my, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{Section: "my", Limit: 50})
	require.NoError(t, err)
	require.Len(t, my, 1)
	assert.Equal(t, ownPack.ID, my[0].ID)

	library, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{Section: "library", Limit: 50})
	require.NoError(t, err)
	require.Len(t, library, 1)
	assert.Equal(t, publishedColleague.ID, library[0].ID)

	students, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{Section: "students", Limit: 50})
	require.NoError(t, err)
	require.Len(t, students, 1)
	assert.Equal(t, studentPack.ID, students[0].ID)
}

func TestRepositoryListReturnsEveryAccessiblePlacement(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, userID, myFolderID := seedPackOwner(t, pool, "placement org")
	libraryFolderID := seedPackLibraryFolder(t, pool, userID)
	studentOneID, studentOneFolderID := seedPackStudentFolder(t, pool, userID, "Student One")
	studentTwoID, studentTwoFolderID := seedPackStudentFolder(t, pool, userID, "Student Two")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	created := createFilteredPack(
		t, repo, userID, myFolderID, "Placement Speech", 5, "easy", config,
	)
	_, err := repo.Publish(t.Context(), userID, created.ID, libraryFolderID, false)
	require.NoError(t, err)
	_, err = repo.Assign(t.Context(), userID, created.ID, []uuid.UUID{studentOneID, studentTwoID})
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO favorite_packs (user_id, pack_id) VALUES ($1, $2)`, userID, created.ID)
	require.NoError(t, err)

	age := 5
	listed, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{
		Query: "pLaCeMeNt", Age: &age, Difficulty: "easy", Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, listed, 4)

	expectedSections := map[uuid.UUID]string{
		myFolderID:         "my",
		libraryFolderID:    "library",
		studentOneFolderID: "students",
		studentTwoFolderID: "students",
	}
	for _, item := range listed {
		assert.Equal(t, created.ID, item.ID)
		assert.True(t, item.IsFavorite)
		assert.Equal(t, expectedSections[item.FolderID], item.Section)
		delete(expectedSections, item.FolderID)
	}
	assert.Empty(t, expectedSections)

	students, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{
		Query: "Placement Speech", Section: "students", Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, students, 2)
	assert.ElementsMatch(t,
		[]uuid.UUID{studentOneFolderID, studentTwoFolderID},
		[]uuid.UUID{students[0].FolderID, students[1].FolderID},
	)

	direct := createFilteredPack(
		t, repo, userID, studentOneFolderID, "Direct Student Pack", 5, "hard", config,
	)
	_, err = repo.Assign(t.Context(), userID, direct.ID, []uuid.UUID{studentOneID})
	require.NoError(t, err)
	directPlacements, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{
		Query: "Direct Student Pack", Section: "students", Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, directPlacements, 1)
	assert.Equal(t, direct.ID, directPlacements[0].ID)
	assert.Equal(t, studentOneFolderID, directPlacements[0].FolderID)
}

func TestRepositoryUpdateChecksFolderOwnershipAtomically(t *testing.T) {
	pool := newPackTestDB(t)
	baseRepo := NewRepository(pool)
	_, ownerID, currentFolderID := seedPackOwner(t, pool, "atomic owner org")
	_, foreignOwnerID, _ := seedPackOwner(t, pool, "atomic foreign org")
	destinationFolderID := seedPackFolder(t, pool, ownerID)
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := baseRepo.Create(context.Background(), ownerID, CreateInput{
		Title: "Pack", FolderID: currentFolderID, Config: config,
	})
	require.NoError(t, err)

	lockTx, err := pool.BeginTx(t.Context(), pgx.TxOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = lockTx.Rollback(t.Context()) })
	var lockedID uuid.UUID
	require.NoError(t, lockTx.QueryRow(
		t.Context(),
		`SELECT id FROM folders WHERE id = $1 FOR UPDATE`,
		destinationFolderID,
	).Scan(&lockedID))

	updateResult := make(chan error, 1)
	go func() {
		_, updateErr := baseRepo.Update(
			context.Background(),
			ownerID,
			created.ID,
			UpdateInput{FolderID: &destinationFolderID},
		)
		updateResult <- updateErr
	}()

	select {
	case updateErr := <-updateResult:
		t.Fatalf("update returned before destination folder lock was released: %v", updateErr)
	case <-time.After(100 * time.Millisecond):
	}

	_, err = lockTx.Exec(
		t.Context(),
		`UPDATE folders SET owner_id = $2 WHERE id = $1`,
		destinationFolderID,
		foreignOwnerID,
	)
	require.NoError(t, err)
	require.NoError(t, lockTx.Commit(t.Context()))
	assert.ErrorIs(t, <-updateResult, ErrFolderNotAllowed)

	fetched, err := baseRepo.Get(context.Background(), ownerID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, currentFolderID, fetched.FolderID)
}

func TestRepositoryMapsMetadataConstraintViolation(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, userID, folderID := seedPackOwner(t, pool, "owner org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := repo.Create(context.Background(), userID, CreateInput{
		Title: "Pack", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)

	invalidAge := 19
	_, err = repo.Update(context.Background(), userID, created.ID, UpdateInput{
		FilterMetadata: &FilterMetadataPatch{
			Age: NullablePatch[int]{Set: true, Value: &invalidAge},
		},
	})
	assert.ErrorIs(t, err, ErrInvalidPackMetadata)
}

func TestRepositoryPublicationIsLinkedIdempotentAndBlocksDelete(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	ownerOrgID, ownerID, folderID := seedPackOwner(t, pool, "owner org")
	readerID, _ := seedPackUserInOrg(t, pool, ownerOrgID, "my")
	_, foreignReaderID, _ := seedPackOwner(t, pool, "foreign reader org")
	libraryFolderID := seedPackLibraryFolder(t, pool, ownerID)
	otherLibraryFolderID := seedPackLibraryFolder(t, pool, ownerID)
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := repo.Create(context.Background(), ownerID, CreateInput{
		Title: "Published", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	_, err = repo.Get(context.Background(), readerID, created.ID)
	assert.ErrorIs(t, err, ErrPackNotFound)

	published, err := repo.Publish(
		context.Background(), ownerID, created.ID, libraryFolderID, false,
	)
	require.NoError(t, err)
	require.NotNil(t, published.LibraryFolderID)
	assert.Equal(t, libraryFolderID, *published.LibraryFolderID)
	require.NotNil(t, published.PublishedAt)
	assert.Equal(t, "published", published.Status)

	again, err := repo.Publish(
		context.Background(), ownerID, created.ID, libraryFolderID, false,
	)
	require.NoError(t, err)
	assert.Equal(t, published.PublishedAt, again.PublishedAt)
	_, err = repo.Publish(
		context.Background(), ownerID, created.ID, otherLibraryFolderID, false,
	)
	assert.ErrorIs(t, err, ErrAlreadyPublished)
	assert.ErrorIs(t, repo.Delete(context.Background(), ownerID, created.ID), ErrPackPublished)

	readable, err := repo.Get(context.Background(), readerID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, readable.ID)
	_, err = repo.Get(context.Background(), foreignReaderID, created.ID)
	assert.ErrorIs(
		t,
		err,
		ErrPackNotFound,
		"published pack must not be accessible outside its organization",
	)

	require.NoError(t, repo.Unpublish(context.Background(), ownerID, created.ID, false))
	require.NoError(t, repo.Unpublish(context.Background(), ownerID, created.ID, false))
	var status string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status FROM packs WHERE id = $1`, created.ID).Scan(&status))
	assert.Equal(t, "draft", status)
	_, err = repo.Get(context.Background(), readerID, created.ID)
	assert.ErrorIs(t, err, ErrPackNotFound)
	require.NoError(t, repo.Delete(context.Background(), ownerID, created.ID))
}

func TestRepositoryPublicationAdminIsScopedToOrganization(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	ownerOrgID, ownerID, folderID := seedPackOwner(t, pool, "owner org")
	_, foreignHeadID, _ := seedPackOwner(t, pool, "foreign head org")
	ownerLibraryID := seedPackLibraryFolder(t, pool, ownerID)
	foreignLibraryID := seedPackLibraryFolder(t, pool, foreignHeadID)
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := repo.Create(context.Background(), ownerID, CreateInput{
		Title: "Scoped", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)

	_, err = repo.Publish(
		context.Background(), foreignHeadID, created.ID, ownerLibraryID, true,
	)
	assert.ErrorIs(t, err, ErrFolderNotAllowed)
	_, err = repo.Publish(
		context.Background(), foreignHeadID, created.ID, foreignLibraryID, true,
	)
	assert.ErrorIs(t, err, ErrFolderNotAllowed)

	sameOrgHeadID := uuid.New()
	_, err = pool.Exec(context.Background(),
		`INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, 'Test User')`, sameOrgHeadID, ownerOrgID)
	require.NoError(t, err)
	published, err := repo.Publish(
		context.Background(), sameOrgHeadID, created.ID, ownerLibraryID, true,
	)
	require.NoError(t, err)
	assert.Equal(t, "published", published.Status)

	err = repo.Unpublish(context.Background(), foreignHeadID, created.ID, true)
	assert.ErrorIs(t, err, ErrPackNotFound)
	fetched, err := repo.Get(context.Background(), ownerID, created.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched.PublishedAt)
	assert.Equal(t, "published", fetched.Status)

	require.NoError(t, repo.Unpublish(context.Background(), sameOrgHeadID, created.ID, true))
	fetched, err = repo.Get(context.Background(), ownerID, created.ID)
	require.NoError(t, err)
	assert.Nil(t, fetched.PublishedAt)
	assert.Equal(t, "draft", fetched.Status)
}

func TestRepositoryAssignmentsAreSnapshotsAndDeleteWithoutOrphans(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	orgID, ownerID, folderID := seedPackOwner(t, pool, "assignment org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := repo.Create(context.Background(), ownerID, CreateInput{
		Title: "Assigned", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	studentIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for index, studentID := range studentIDs {
		_, err = pool.Exec(context.Background(), `
			INSERT INTO students
				(id, defectologist_id, email_encrypted, name, status)
			VALUES ($1, $2, '\x00', $3, 'active')`,
			studentID, ownerID, "Student "+string(rune('A'+index)))
		require.NoError(t, err)
	}

	assigned, err := repo.Assign(context.Background(), ownerID, created.ID, studentIDs)
	require.NoError(t, err)
	require.Len(t, assigned, 2)
	assert.JSONEq(t, string(config), string(assigned[0].Config))

	listed, err := repo.ListAdaptations(t.Context(), ownerID, created.ID)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.ElementsMatch(t, studentIDs, []uuid.UUID{listed[0].StudentID, listed[1].StudentID})

	fetched, err := repo.GetAdaptation(t.Context(), ownerID, assigned[0].ID)
	require.NoError(t, err)
	assert.Equal(t, assigned[0].ID, fetched.ID)
	assert.Equal(t, created.ID, fetched.PackID)
	assert.Equal(t, studentIDs[0], fetched.StudentID)
	assert.JSONEq(t, string(config), string(fetched.Config))

	mediaID := uuid.New()
	_, err = pool.Exec(t.Context(), `
		INSERT INTO media_files (
			id, org_id, uploader_id, name, sha256, mime_type, media_type, size_bytes, minio_key
		)
		VALUES ($1, $2, $3, 'media.png', $4, 'image/png', 'image', 4, $5)`,
		mediaID, orgID, ownerID, "adaptation-media-sha", "media/"+mediaID.String(),
	)
	require.NoError(t, err)
	updatedConfig := json.RawMessage(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":2},
		"blocks":[{
			"id":"block","type":"grid",
			"elements":[{"id":"image","kind":"image","media_id":"` + mediaID.String() + `"}]
		}]
	}`)
	service := NewContentService(repo, nil, nil, nil)
	updated, err := service.UpdateAdaptationConfig(
		packContext(ownerID), assigned[0].ID, updatedConfig,
	)
	require.NoError(t, err)
	assert.Equal(t, assigned[0].ID, updated.ID)
	assert.JSONEq(t, string(updatedConfig), string(updated.Config))

	var adaptationUsageCount int
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT count(*) FROM media_usages
		WHERE source_type = 'pack_adaptation' AND source_id = $1`, assigned[0].ID,
	).Scan(&adaptationUsageCount))
	assert.Equal(t, 1, adaptationUsageCount)

	_, err = service.UpdateAdaptationConfig(packContext(ownerID), assigned[0].ID, json.RawMessage(`{}`))
	assertAppErrorStatus(t, err, apperr.ErrBadRequest.HTTPStatus)
	unchanged, err := repo.GetAdaptation(t.Context(), ownerID, assigned[0].ID)
	require.NoError(t, err)
	assert.JSONEq(t, string(updatedConfig), string(unchanged.Config))

	_, foreignID, _ := seedPackOwner(t, pool, "foreign assignment org")
	_, err = repo.GetAdaptation(t.Context(), foreignID, assigned[0].ID)
	assert.ErrorIs(t, err, ErrAdaptationNotFound)
	_, err = repo.ListAdaptations(t.Context(), foreignID, created.ID)
	assert.ErrorIs(t, err, ErrPackNotFound)
	_, err = service.UpdateAdaptationConfig(packContext(foreignID), assigned[0].ID, updatedConfig)
	assertAppErrorStatus(t, err, apperr.ErrNotFound.HTTPStatus)

	emptyPack, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "Unassigned", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	empty, err := repo.ListAdaptations(t.Context(), ownerID, emptyPack.ID)
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.NotNil(t, empty)

	var adaptationCount int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*) FROM pack_adaptations WHERE pack_id = $1`, created.ID,
	).Scan(&adaptationCount))
	assert.Equal(t, 2, adaptationCount)

	require.NoError(t, repo.Unassign(context.Background(), ownerID, created.ID, studentIDs[0]))
	require.NoError(t, repo.Delete(context.Background(), ownerID, created.ID))
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*) FROM pack_adaptations WHERE pack_id = $1`, created.ID,
	).Scan(&adaptationCount))
	assert.Zero(t, adaptationCount)
	_, err = pool.Exec(t.Context(), `DELETE FROM media_files WHERE id = $1`, mediaID)
	require.NoError(t, err)
}

func TestRepositoryAdaptationArchiveUsesSnapshotMediaAndChecksAccess(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	orgID, ownerID, folderID := seedPackOwner(t, pool, "adaptation export org")
	_, foreignID, _ := seedPackOwner(t, pool, "foreign adaptation export org")
	studentID, _ := seedPackStudentFolder(t, pool, ownerID, "Export Student")
	mediaID := uuid.New()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO media_files (
			id, org_id, uploader_id, name, sha256, mime_type, media_type, size_bytes, minio_key
		)
		VALUES ($1, $2, $3, 'media.png', $4, 'image/png', 'image', 4, $5)`,
		mediaID, orgID, ownerID, "adaptation-media-sha", "media/"+mediaID.String(),
	)
	require.NoError(t, err)
	configWithMedia := []byte(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":1},
		"blocks":[{
			"id":"block","type":"grid",
			"elements":[{"id":"image","kind":"image","media_id":"` + mediaID.String() + `"}]
		}]
	}`)
	created, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "Adapted pack", FolderID: folderID, Config: configWithMedia,
	})
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO media_usages (media_id, source_type, source_id)
		VALUES ($1, 'pack', $2)`, mediaID, created.ID)
	require.NoError(t, err)
	assigned, err := repo.Assign(t.Context(), ownerID, created.ID, []uuid.UUID{studentID})
	require.NoError(t, err)
	require.Len(t, assigned, 1)

	emptyConfig := []byte(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":1},
		"blocks":[]
	}`)
	_, err = repo.SaveConfig(t.Context(), ownerID, created.ID, emptyConfig, nil)
	require.NoError(t, err)

	data, files, err := repo.AdaptationArchiveData(t.Context(), ownerID, assigned[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "Adapted pack", data.Title)
	assert.JSONEq(t, string(configWithMedia), string(data.Config))
	require.Len(t, files, 1)
	assert.Equal(t, mediaID, files[0].ID)

	_, _, err = repo.AdaptationArchiveData(t.Context(), foreignID, assigned[0].ID)
	assert.ErrorIs(t, err, ErrAdaptationNotFound)
}

func newPackTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, cleanup := testutil.NewPostgres(t)
	t.Cleanup(cleanup)

	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})
	require.NoError(t, applyPackMigrations(db))
	return pool
}

func applyPackMigrations(db *sql.DB) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, "../../migrations")
}

func seedPackOwner(t *testing.T, pool *pgxpool.Pool, orgName string) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	orgID := uuid.New()
	userID := uuid.New()
	folderID := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO organizations (id, name) VALUES ($1, $2)`, orgID, orgName)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, 'Test User')`, userID, orgID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO folders (id, org_id, owner_id, section, kind, name, depth)
		VALUES ($1, $2, $3, 'my', 'folder', 'Root', 0)`, folderID, orgID, userID)
	require.NoError(t, err)
	return orgID, userID, folderID
}

func seedPackFolder(t *testing.T, pool *pgxpool.Pool, ownerID uuid.UUID) uuid.UUID {
	t.Helper()
	folderID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO folders (id, org_id, owner_id, section, kind, name, depth)
		SELECT $1, org_id, id, 'my', 'folder', 'Folder', 0
		FROM users WHERE id = $2`, folderID, ownerID)
	require.NoError(t, err)
	return folderID
}

func seedPackSectionFolder(
	t *testing.T,
	pool *pgxpool.Pool,
	ownerID uuid.UUID,
	section string,
) uuid.UUID {
	t.Helper()
	folderID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO folders (id, org_id, owner_id, section, kind, name, depth)
		SELECT $1, org_id, id, $3, 'folder', 'Folder', 0
		FROM users WHERE id = $2`, folderID, ownerID, section)
	require.NoError(t, err)
	return folderID
}

func seedPackUserInOrg(
	t *testing.T,
	pool *pgxpool.Pool,
	orgID uuid.UUID,
	section string,
) (uuid.UUID, uuid.UUID) {
	t.Helper()
	userID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, 'Test User')`, userID, orgID)
	require.NoError(t, err)
	return userID, seedPackSectionFolder(t, pool, userID, section)
}

func seedPackStudentFolder(
	t *testing.T,
	pool *pgxpool.Pool,
	ownerID uuid.UUID,
	name string,
) (uuid.UUID, uuid.UUID) {
	t.Helper()
	studentID := uuid.New()
	folderID := uuid.New()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO students (
			id, defectologist_id, email_encrypted, name, status
		) VALUES ($1, $2, '\x00', $3, 'active')`, studentID, ownerID, name)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO folders (
			id, org_id, owner_id, section, kind, student_id, name, depth
		)
		SELECT $1, org_id, id, 'students', 'student', $3, $4, 0
		FROM users WHERE id = $2`, folderID, ownerID, studentID, name)
	require.NoError(t, err)
	return studentID, folderID
}

func createFilteredPack(
	t *testing.T,
	repo *Repository,
	userID, folderID uuid.UUID,
	title string,
	age int,
	difficulty string,
	config []byte,
) *Pack {
	t.Helper()
	created, err := repo.Create(t.Context(), userID, CreateInput{
		Title: title, FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	updated, err := repo.Update(t.Context(), userID, created.ID, UpdateInput{
		FilterMetadata: &FilterMetadataPatch{
			Age:        NullablePatch[int]{Set: true, Value: &age},
			Difficulty: NullablePatch[string]{Set: true, Value: &difficulty},
		},
	})
	require.NoError(t, err)
	return updated
}

func listItemsByID(items []*ListItem) map[uuid.UUID]*ListItem {
	result := make(map[uuid.UUID]*ListItem, len(items))
	for _, item := range items {
		result[item.ID] = item
	}
	return result
}

func seedPackLibraryFolder(t *testing.T, pool *pgxpool.Pool, ownerID uuid.UUID) uuid.UUID {
	t.Helper()
	folderID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO folders (id, org_id, owner_id, section, kind, name, depth)
		SELECT $1, org_id, id, 'library', 'folder', 'Library', 0
		FROM users WHERE id = $2`, folderID, ownerID)
	require.NoError(t, err)
	return folderID
}

func stringPtr(value string) *string {
	return &value
}

// TestRepositoryListFiltersByStudent: наборы ученика — это и его папка, и
// вложенные в неё папки, и адаптации из чужих разделов.
func TestRepositoryListFiltersByStudent(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, userID, myFolderID := seedPackOwner(t, pool, "student filter org")
	studentA, folderA := seedPackStudentFolder(t, pool, userID, "Аня")
	studentB, folderB := seedPackStudentFolder(t, pool, userID, "Боря")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	var nestedID uuid.UUID
	require.NoError(t, pool.QueryRow(t.Context(), `
		INSERT INTO folders (org_id, owner_id, parent_id, section, kind, name, depth)
		SELECT org_id, id, $2, 'students', 'folder', 'Занятия', 1
		FROM users WHERE id = $1
		RETURNING id`, userID, folderA).Scan(&nestedID))

	direct, err := repo.Create(t.Context(), userID, CreateInput{
		Title: "Прямо в папке", FolderID: folderA, Config: config,
	})
	require.NoError(t, err)
	nested, err := repo.Create(t.Context(), userID, CreateInput{
		Title: "Во вложенной", FolderID: nestedID, Config: config,
	})
	require.NoError(t, err)
	other, err := repo.Create(t.Context(), userID, CreateInput{
		Title: "У другого ученика", FolderID: folderB, Config: config,
	})
	require.NoError(t, err)
	mine, err := repo.Create(t.Context(), userID, CreateInput{
		Title: "Мой набор", FolderID: myFolderID, Config: config,
	})
	require.NoError(t, err)
	_, err = repo.Assign(t.Context(), userID, mine.ID, []uuid.UUID{studentA})
	require.NoError(t, err)

	listed, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{StudentID: &studentA, Limit: 50})
	require.NoError(t, err)
	items := listItemsByID(listed)
	assert.Contains(t, items, direct.ID)
	assert.Contains(t, items, nested.ID, "вложенная папка принадлежит тому же ученику")
	assert.Contains(t, items, mine.ID, "адаптация из «Моих наборов» тоже относится к ученику")
	assert.NotContains(t, items, other.ID)

	forB, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{StudentID: &studentB, Limit: 50})
	require.NoError(t, err)
	require.Len(t, forB, 1)
	assert.Equal(t, other.ID, forB[0].ID)

	// Фильтр складывается с разделом. Адаптация числится в разделе
	// students по папке ученика, хотя сам набор лежит в «Моих наборах»,
	// поэтому из выдачи она не выпадает.
	scoped, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{
		StudentID: &studentA, Section: "students", Limit: 50,
	})
	require.NoError(t, err)
	scopedItems := listItemsByID(scoped)
	assert.Contains(t, scopedItems, direct.ID)
	assert.Contains(t, scopedItems, nested.ID)
	require.Contains(t, scopedItems, mine.ID)
	assert.Equal(t, "students", scopedItems[mine.ID].Section)

	inMy, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{
		StudentID: &studentA, Section: "my", Limit: 50,
	})
	require.NoError(t, err)
	assert.Empty(t, inMy, "в «Моих наборах» у набора нет ученика")

	unknown := uuid.New()
	empty, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{StudentID: &unknown, Limit: 50})
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// TestRepositoryListSorts проверяет белый список сортировок: колонка и
// направление приходят от клиента, но в SQL попадают только свои.
func TestRepositoryListSorts(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, userID, folderID := seedPackOwner(t, pool, "sort org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	titles := []string{"Собака", "азбука", "Мячик"}
	created := make([]*Pack, 0, len(titles))
	for _, title := range titles {
		pack, err := repo.Create(t.Context(), userID, CreateInput{
			Title: title, FolderID: folderID, Config: config,
		})
		require.NoError(t, err)
		created = append(created, pack)
	}

	byTitle, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{
		SortBy: "title", Order: "asc", Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, byTitle, 3)
	assert.Equal(t, []string{"азбука", "Мячик", "Собака"},
		[]string{byTitle[0].Title, byTitle[1].Title, byTitle[2].Title})

	desc, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{
		SortBy: "title", Order: "desc", Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, desc, 3)
	assert.Equal(t, "Собака", desc[0].Title)

	byCreated, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{
		SortBy: "created_at", Order: "asc", Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, byCreated, 3)
	assert.Equal(t, created[0].ID, byCreated[0].ID)

	// По умолчанию — свежие сверху, как было до появления сортировок.
	byDefault, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{Limit: 50})
	require.NoError(t, err)
	require.Len(t, byDefault, 3)
	assert.Equal(t, created[2].ID, byDefault[0].ID)
}

func TestRepositoryPackShareOutboxPersistsAndReclaimsLease(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, ownerID, _ := seedPackOwner(t, pool, "share outbox org")

	now := time.Now().UTC().Truncate(time.Microsecond)
	job := shareJobRecord{
		ID:            uuid.New(),
		OwnerID:       ownerID,
		PackID:        uuid.New(),
		StudentID:     uuid.New(),
		RequestID:     "req-1",
		Status:        ShareTaskQueued,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	require.NoError(t, repo.EnqueueShareJob(t.Context(), job))

	stored, err := repo.GetShareJob(t.Context(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, ShareTaskQueued, stored.Status)
	assert.Equal(t, ownerID, stored.OwnerID)
	assert.Equal(t, "req-1", stored.RequestID)

	claimed, err := repo.ClaimShareJob(t.Context(), time.Minute, 5)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, job.ID, claimed.ID)
	assert.Equal(t, ShareTaskProcessing, claimed.Status)
	assert.NotEqual(t, uuid.Nil, claimed.LeaseToken)
	assert.Equal(t, 1, claimed.Attempts)

	second, err := repo.ClaimShareJob(t.Context(), time.Minute, 5)
	require.NoError(t, err)
	assert.Nil(t, second, "an active lease must prevent duplicate processing")

	_, err = pool.Exec(t.Context(), `
		UPDATE pack_share_jobs
		SET lease_until = now() - interval '1 second'
		WHERE id = $1
	`, job.ID)
	require.NoError(t, err)

	reclaimed, err := repo.ClaimShareJob(t.Context(), time.Minute, 5)
	require.NoError(t, err)
	require.NotNil(t, reclaimed)
	assert.Equal(t, job.ID, reclaimed.ID)
	assert.NotEqual(t, claimed.LeaseToken, reclaimed.LeaseToken)
	assert.Equal(t, 2, reclaimed.Attempts)

	require.ErrorIs(t,
		repo.CompleteShareJob(t.Context(), job.ID, claimed.LeaseToken),
		errShareJobLeaseLost,
		"a stale worker must not finalize a job after another worker reclaimed it",
	)

	require.NoError(t, repo.RequeueShareJob(
		t.Context(),
		job.ID,
		reclaimed.LeaseToken,
		"retry after restart",
		"context deadline exceeded",
		0,
	))

	requeued, err := repo.GetShareJob(t.Context(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, ShareTaskQueued, requeued.Status)
	assert.Equal(t, "retry after restart", requeued.Message)

	claimedAgain, err := repo.ClaimShareJob(t.Context(), time.Minute, 5)
	require.NoError(t, err)
	require.NotNil(t, claimedAgain)
	require.NoError(t, repo.CompleteShareJob(t.Context(), job.ID, claimedAgain.LeaseToken))

	completed, err := repo.GetShareJob(t.Context(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, ShareTaskSent, completed.Status)

	deleted, err := repo.PruneShareJobs(t.Context(), time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	_, err = repo.GetShareJob(t.Context(), job.ID)
	assert.ErrorIs(t, err, errShareJobNotFound)
}

func TestRepositoryPackShareOutboxStopsAfterMaxAttempts(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, ownerID, _ := seedPackOwner(t, pool, "share max attempts org")

	now := time.Now().UTC()
	job := shareJobRecord{
		ID: uuid.New(), OwnerID: ownerID, PackID: uuid.New(), StudentID: uuid.New(),
		Status: ShareTaskQueued, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.EnqueueShareJob(t.Context(), job))

	claimed, err := repo.ClaimShareJob(t.Context(), time.Minute, 1)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, 1, claimed.Attempts)
	require.NoError(t, repo.RequeueShareJob(
		t.Context(), job.ID, claimed.LeaseToken,
		"email delivery failed; retrying", "context deadline exceeded", 0,
	))

	next, err := repo.ClaimShareJob(t.Context(), time.Minute, 1)
	require.NoError(t, err)
	assert.Nil(t, next, "exhausted job must not be claimed again")

	stored, err := repo.GetShareJob(t.Context(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, ShareTaskFailed, stored.Status)
	assert.Equal(t, 1, stored.Attempts)
	assert.Equal(t, "context deadline exceeded", stored.LastError)
}

func TestRepositoryPackShareOutboxDoesNotResendAfterSMTPWasAccepted(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, ownerID, _ := seedPackOwner(t, pool, "share smtp accepted org")

	now := time.Now().UTC()
	job := shareJobRecord{
		ID: uuid.New(), OwnerID: ownerID, PackID: uuid.New(), StudentID: uuid.New(),
		Status: ShareTaskQueued, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.EnqueueShareJob(t.Context(), job))

	claimed, err := repo.ClaimShareJob(t.Context(), time.Minute, 1)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.NoError(t, repo.MarkShareJobEmailSent(t.Context(), job.ID, claimed.LeaseToken))

	_, err = pool.Exec(t.Context(), `
		UPDATE pack_share_jobs SET lease_until = now() - interval '1 second' WHERE id = $1
	`, job.ID)
	require.NoError(t, err)

	reclaimed, err := repo.ClaimShareJob(t.Context(), time.Minute, 1)
	require.NoError(t, err)
	require.NotNil(t, reclaimed)
	require.NotNil(t, reclaimed.EmailSentAt)
	assert.Equal(t, 1, reclaimed.Attempts, "finalization reclaim must not consume another delivery attempt")
	require.NoError(t, repo.CompleteShareJob(t.Context(), job.ID, reclaimed.LeaseToken))

	stored, err := repo.GetShareJob(t.Context(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, ShareTaskSent, stored.Status)
}
