package pack

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestRepositoryPutFavoriteIsIdempotent(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, userID, folderID := seedPackOwner(t, pool, "favorite org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := repo.Create(t.Context(), userID, CreateInput{Title: "Pack", FolderID: folderID, Config: config})
	require.NoError(t, err)

	require.NoError(t, repo.PutFavorite(t.Context(), userID, created.ID))
	require.NoError(t, repo.PutFavorite(t.Context(), userID, created.ID))

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM favorite_packs WHERE user_id = $1 AND pack_id = $2`, userID, created.ID,
	).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestRepositoryPutFavoriteAllowsPublishedPack(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	orgID, ownerID, ownerFolderID := seedPackOwner(t, pool, "favorite publish org")
	libraryFolderID := seedPackLibraryFolder(t, pool, ownerID)
	viewerID, _ := seedPackUserInOrg(t, pool, orgID, "my")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := repo.Create(t.Context(), ownerID, CreateInput{Title: "Pack", FolderID: ownerFolderID, Config: config})
	require.NoError(t, err)
	_, err = repo.Publish(t.Context(), ownerID, created.ID, libraryFolderID, false)
	require.NoError(t, err)

	require.NoError(t, repo.PutFavorite(t.Context(), viewerID, created.ID))
}

func TestRepositoryPutFavoriteRejectsPublishedPackFromForeignOrg(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, ownerID, ownerFolderID := seedPackOwner(t, pool, "favorite publish foreign org")
	libraryFolderID := seedPackLibraryFolder(t, pool, ownerID)
	_, viewerID, _ := seedPackOwner(t, pool, "favorite viewer foreign org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := repo.Create(t.Context(), ownerID, CreateInput{Title: "Pack", FolderID: ownerFolderID, Config: config})
	require.NoError(t, err)
	_, err = repo.Publish(t.Context(), ownerID, created.ID, libraryFolderID, false)
	require.NoError(t, err)

	err = repo.PutFavorite(t.Context(), viewerID, created.ID)

	assert.ErrorIs(t, err, ErrPackNotFound)
}

func TestRepositoryPutFavoriteRejectsInaccessiblePack(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	orgID, ownerID, ownerFolderID := seedPackOwner(t, pool, "favorite private org")
	viewerID, _ := seedPackUserInOrg(t, pool, orgID, "my")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := repo.Create(t.Context(), ownerID, CreateInput{Title: "Pack", FolderID: ownerFolderID, Config: config})
	require.NoError(t, err)

	err = repo.PutFavorite(t.Context(), viewerID, created.ID)

	assert.ErrorIs(t, err, ErrPackNotFound)
}

func TestRepositoryPutFavoriteRejectsUnknownPack(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, userID, _ := seedPackOwner(t, pool, "favorite unknown org")

	err := repo.PutFavorite(t.Context(), userID, uuid.New())

	assert.ErrorIs(t, err, ErrPackNotFound)
}

func TestRepositoryDeleteFavoriteIsIdempotent(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, userID, folderID := seedPackOwner(t, pool, "unfavorite org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	created, err := repo.Create(t.Context(), userID, CreateInput{Title: "Pack", FolderID: folderID, Config: config})
	require.NoError(t, err)
	require.NoError(t, repo.PutFavorite(t.Context(), userID, created.ID))

	require.NoError(t, repo.DeleteFavorite(t.Context(), userID, created.ID))
	require.NoError(t, repo.DeleteFavorite(t.Context(), userID, created.ID))
	require.NoError(t, repo.DeleteFavorite(t.Context(), userID, uuid.New()))

	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM favorite_packs WHERE user_id = $1 AND pack_id = $2`, userID, created.ID,
	).Scan(&count))
	assert.Zero(t, count)
}

func TestRepositoryListFavoritesReturnsAccessibleBookmarksInOrder(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	orgID, userID, myFolderID := seedPackOwner(t, pool, "list favorite org")
	colleagueID, colleagueFolderID := seedPackUserInOrg(t, pool, orgID, "my")
	libraryFolderID := seedPackLibraryFolder(t, pool, colleagueID)
	_, foreignID, foreignFolderID := seedPackOwner(t, pool, "list favorite foreign org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	ownPack, err := repo.Create(t.Context(), userID, CreateInput{Title: "Own", FolderID: myFolderID, Config: config})
	require.NoError(t, err)
	publishedPack, err := repo.Create(
		t.Context(), colleagueID, CreateInput{Title: "Published", FolderID: colleagueFolderID, Config: config},
	)
	require.NoError(t, err)
	_, err = repo.Publish(t.Context(), colleagueID, publishedPack.ID, libraryFolderID, false)
	require.NoError(t, err)
	privatePack, err := repo.Create(
		t.Context(), colleagueID, CreateInput{Title: "Private", FolderID: colleagueFolderID, Config: config},
	)
	require.NoError(t, err)
	foreignPack, err := repo.Create(
		t.Context(), foreignID, CreateInput{Title: "Foreign", FolderID: foreignFolderID, Config: config},
	)
	require.NoError(t, err)

	require.NoError(t, repo.PutFavorite(t.Context(), userID, ownPack.ID))
	require.NoError(t, repo.PutFavorite(t.Context(), userID, publishedPack.ID))
	// Simulate stale bookmarks that must never surface: a still-private colleague pack and
	// a pack that belongs to a different organization entirely.
	_, err = pool.Exec(t.Context(),
		`INSERT INTO favorite_packs (user_id, pack_id) VALUES ($1, $2), ($1, $3)`,
		userID, privatePack.ID, foreignPack.ID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`UPDATE favorite_packs SET created_at = to_timestamp(1) WHERE user_id = $1 AND pack_id = $2`,
		userID, ownPack.ID)
	require.NoError(t, err)

	listed, _, err := repo.ListWithTotal(t.Context(), userID, ListInput{Limit: 50})

	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, publishedPack.ID, listed[0].ID)
	assert.Equal(t, libraryFolderID, listed[0].FolderID)
	assert.Equal(t, "library", listed[0].Section)
	assert.True(t, listed[0].IsFavorite)
	assert.Equal(t, ownPack.ID, listed[1].ID)
	assert.Equal(t, myFolderID, listed[1].FolderID)
	assert.Equal(t, "my", listed[1].Section)
}

func TestRepositoryListFavoritesPaginates(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, userID, folderID := seedPackOwner(t, pool, "paginate favorite org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	created := make([]*Pack, 0, 3)
	for i := 0; i < 3; i++ {
		pack, err := repo.Create(t.Context(), userID, CreateInput{Title: "Pack", FolderID: folderID, Config: config})
		require.NoError(t, err)
		require.NoError(t, repo.PutFavorite(t.Context(), userID, pack.ID))
		created = append(created, pack)
	}
	for index, pack := range created {
		_, err := pool.Exec(t.Context(),
			`UPDATE favorite_packs SET created_at = to_timestamp($2) WHERE user_id = $1 AND pack_id = $3`,
			userID, index+1, pack.ID)
		require.NoError(t, err)
	}

	listed, total, err := repo.ListWithTotal(t.Context(), userID, ListInput{Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, created[1].ID, listed[0].ID)
	assert.Equal(t, 3, total)

	beyond, total, err := repo.ListWithTotal(t.Context(), userID, ListInput{Limit: 1, Offset: 10})
	require.NoError(t, err)
	assert.Empty(t, beyond)
	assert.Equal(t, 3, total)
}
func TestRepositoryListWithTotalConsistentUnderConcurrentWrites(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, userID, folderID := seedPackOwner(t, pool, "concurrent favorite org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	const stableCount = 5
	for i := 0; i < stableCount; i++ {
		pack, err := repo.Create(t.Context(), userID, CreateInput{Title: "Stable", FolderID: folderID, Config: config})
		require.NoError(t, err)
		require.NoError(t, repo.PutFavorite(t.Context(), userID, pack.ID))
	}

	toggled, err := repo.Create(t.Context(), userID, CreateInput{Title: "Toggled", FolderID: folderID, Config: config})
	require.NoError(t, err)

	const iterations = 150
	group, ctx := errgroup.WithContext(t.Context())
	// Горутина добавляет-удаляет избранный пакет Toggled.
	group.Go(func() error {
		for i := 0; i < iterations; i++ {
			if err := repo.PutFavorite(ctx, userID, toggled.ID); err != nil {
				return fmt.Errorf("put favorite: %w", err)
			}
			if err := repo.DeleteFavorite(ctx, userID, toggled.ID); err != nil {
				return fmt.Errorf("delete favorite: %w", err)
			}
		}
		return nil
	})
	// 4 горутины проверяют, что возвращённые записи в списке в точности совпадают с общим числом избранных пакетов.
	// Total может быть только stableCount либо stableCount+1 (+ toggled).
	const readers = 4
	for r := 0; r < readers; r++ {
		group.Go(func() error {
			for i := 0; i < iterations; i++ {
				items, total, err := repo.ListFavoritesWithTotal(ctx, userID, ListInput{Limit: 100})
				if err != nil {
					return fmt.Errorf("list with total: %w", err)
				}
				if len(items) != total {
					return fmt.Errorf("inconsistent snapshot: len(items)=%d total=%d", len(items), total)
				}
				if total != stableCount && total != stableCount+1 {
					return fmt.Errorf("unexpected total %d, want %d or %d", total, stableCount, stableCount+1)
				}
			}
			return nil
		})
	}

	require.NoError(t, group.Wait())
}
