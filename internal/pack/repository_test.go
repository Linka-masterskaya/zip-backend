package pack

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

	ageMin, ageMax := 5, 8
	difficulty := "medium"
	notes := "notes"
	goals := []string{"speech", "attention"}
	title := "Updated pack"
	updated, err := repo.Update(context.Background(), userID, created.ID, UpdateInput{
		Title: &title,
		FilterMetadata: &FilterMetadataPatch{
			AgeMin:     NullablePatch[int]{Set: true, Value: &ageMin},
			AgeMax:     NullablePatch[int]{Set: true, Value: &ageMax},
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
			AgeMin:     NullablePatch[int]{Set: true},
			AgeMax:     NullablePatch[int]{Set: true},
			Difficulty: NullablePatch[string]{Set: true},
		},
		Notes: NullablePatch[string]{Set: true},
	})
	require.NoError(t, err)
	assert.Nil(t, cleared.AgeMin)
	assert.Nil(t, cleared.AgeMax)
	assert.Nil(t, cleared.Difficulty)
	assert.Empty(t, cleared.Notes)
	assert.JSONEq(t, string(config), string(cleared.Config))

	listed, err := repo.List(context.Background(), userID, folderID, ListInput{Limit: 50})
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
	_, err = repo.List(context.Background(), ownerID, foreignFolderID, ListInput{Limit: 50})
	assert.ErrorIs(t, err, ErrFolderNotAllowed)
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

func TestRepositoryListUsesLimitAndOffset(t *testing.T) {
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

	listed, err := repo.List(context.Background(), userID, folderID, ListInput{Limit: 1, Offset: 1})

	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, created[1].ID, listed[0].ID)
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

	raceDB := &folderOwnershipRaceDB{
		pool: pool, folderID: destinationFolderID, newOwnerID: foreignOwnerID,
	}
	repo := &Repository{db: raceDB}
	_, err = repo.Update(context.Background(), ownerID, created.ID, UpdateInput{
		FolderID: &destinationFolderID,
	})

	assert.ErrorIs(t, err, ErrPackNotFound)
	require.NoError(t, raceDB.err)
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

	ageMax := 5
	_, err = repo.Update(context.Background(), userID, created.ID, UpdateInput{
		FilterMetadata: &FilterMetadataPatch{AgeMax: NullablePatch[int]{Set: true, Value: &ageMax}},
	})
	require.NoError(t, err)
	ageMin := 8
	_, err = repo.Update(context.Background(), userID, created.ID, UpdateInput{
		FilterMetadata: &FilterMetadataPatch{AgeMin: NullablePatch[int]{Set: true, Value: &ageMin}},
	})
	assert.ErrorIs(t, err, ErrInvalidPackMetadata)
}

func TestRepositoryPublicationIsLinkedIdempotentAndBlocksDelete(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, ownerID, folderID := seedPackOwner(t, pool, "owner org")
	_, readerID, _ := seedPackOwner(t, pool, "reader org")
	libraryFolderID := seedPackLibraryFolder(t, pool, ownerID)
	otherLibraryFolderID := seedPackLibraryFolder(t, pool, ownerID)
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := repo.Create(context.Background(), ownerID, CreateInput{
		Title: "Published", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)

	published, err := repo.Publish(
		context.Background(), ownerID, created.ID, libraryFolderID, false,
	)
	require.NoError(t, err)
	require.NotNil(t, published.LibraryFolderID)
	assert.Equal(t, libraryFolderID, *published.LibraryFolderID)
	require.NotNil(t, published.PublishedAt)

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

	require.NoError(t, repo.Unpublish(context.Background(), ownerID, created.ID, false))
	require.NoError(t, repo.Unpublish(context.Background(), ownerID, created.ID, false))
	_, err = repo.Get(context.Background(), readerID, created.ID)
	assert.ErrorIs(t, err, ErrPackNotFound)
	require.NoError(t, repo.Delete(context.Background(), ownerID, created.ID))
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
	_, err = pool.Exec(ctx, `INSERT INTO users (id, org_id) VALUES ($1, $2)`, userID, orgID)
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

type folderOwnershipRaceDB struct {
	pool       *pgxpool.Pool
	folderID   uuid.UUID
	newOwnerID uuid.UUID
	queryRows  int
	err        error
}

func (d *folderOwnershipRaceDB) Exec(
	ctx context.Context,
	query string,
	args ...any,
) (pgconn.CommandTag, error) {
	return d.pool.Exec(ctx, query, args...)
}

func (d *folderOwnershipRaceDB) Query(
	ctx context.Context,
	query string,
	args ...any,
) (pgx.Rows, error) {
	return d.pool.Query(ctx, query, args...)
}

func (d *folderOwnershipRaceDB) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	d.queryRows++
	if d.queryRows == 2 {
		_, d.err = d.pool.Exec(ctx, `UPDATE folders SET owner_id = $2 WHERE id = $1`, d.folderID, d.newOwnerID)
	}
	return d.pool.QueryRow(ctx, query, args...)
}

func stringPtr(value string) *string {
	return &value
}
