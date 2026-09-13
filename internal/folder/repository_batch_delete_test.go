package folder

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeleteBatchRemovesNestedFoldersBottomUp: папка, чьи подпапки отмечены в
// той же пачке, освобождается вместе с ними. Порядок удаления снизу вверх,
// иначе RESTRICT на folders.parent_id уронил бы транзакцию.
func TestDeleteBatchRemovesNestedFoldersBottomUp(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "batch owner")
	service := NewService(NewRepository(pool), 0)
	ctx := folderContext(ownerID)

	root, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Root",
	})
	require.NoError(t, err)
	child, err := service.Create(ctx, CreateInput{
		ParentID: &root.ID, Section: SectionMy, Kind: KindFolder, Name: "Child",
	})
	require.NoError(t, err)
	grandchild, err := service.Create(ctx, CreateInput{
		ParentID: &child.ID, Section: SectionMy, Kind: KindFolder, Name: "Grandchild",
	})
	require.NoError(t, err)

	// Идентификаторы идут сверху вниз: правильный порядок обеспечивает сам
	// репозиторий, а не клиент.
	result, err := service.DeleteBatch(ctx, []uuid.UUID{root.ID, child.ID, grandchild.ID}, false)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{root.ID, child.ID, grandchild.ID}, result.Deleted)
	assert.Empty(t, result.Skipped)
	assert.Equal(t, 0, countFolders(t, pool))
}

// TestDeleteBatchSkipsNotEmptyAndForeign: одна проблемная папка не мешает
// остальным уйти, а причина пропуска приходит клиенту.
func TestDeleteBatchSkipsNotEmptyAndForeign(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "skip owner")
	foreignID := seedFolderUser(t, pool, "skip foreign")
	service := NewService(NewRepository(pool), 0)
	ctx := folderContext(ownerID)

	empty, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Empty",
	})
	require.NoError(t, err)
	withPack, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "With pack",
	})
	require.NoError(t, err)
	seedFolderPack(t, pool, ownerID, withPack.ID)
	foreign, err := service.Create(folderContext(foreignID), CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Foreign",
	})
	require.NoError(t, err)
	missingID := uuid.New()

	result, err := service.DeleteBatch(
		ctx, []uuid.UUID{empty.ID, withPack.ID, foreign.ID, missingID}, false,
	)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{empty.ID}, result.Deleted)
	assert.ElementsMatch(t, []uuid.UUID{withPack.ID, foreign.ID, missingID}, skippedIDs(result))
	assert.Equal(t, 2, countFolders(t, pool))
}

// TestDeleteBatchDryRunChangesNothing: холостой прогон считает тот же
// результат, что и настоящее удаление, и ничего не трогает.
func TestDeleteBatchDryRunChangesNothing(t *testing.T) {
	pool := folderTestDB(t)
	ownerID := seedFolderUser(t, pool, "dry run owner")
	service := NewService(NewRepository(pool), 0)
	ctx := folderContext(ownerID)

	root, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "Root",
	})
	require.NoError(t, err)
	child, err := service.Create(ctx, CreateInput{
		ParentID: &root.ID, Section: SectionMy, Kind: KindFolder, Name: "Child",
	})
	require.NoError(t, err)
	withPack, err := service.Create(ctx, CreateInput{
		Section: SectionMy, Kind: KindFolder, Name: "With pack",
	})
	require.NoError(t, err)
	seedFolderPack(t, pool, ownerID, withPack.ID)

	ids := []uuid.UUID{root.ID, child.ID, withPack.ID}
	dry, err := service.DeleteBatch(ctx, ids, true)
	require.NoError(t, err)
	assert.True(t, dry.DryRun)
	assert.ElementsMatch(t, []uuid.UUID{root.ID, child.ID}, dry.Deleted)
	assert.Equal(t, []uuid.UUID{withPack.ID}, skippedIDs(dry))
	assert.Equal(t, 3, countFolders(t, pool))

	real, err := service.DeleteBatch(ctx, ids, false)
	require.NoError(t, err)
	assert.ElementsMatch(t, dry.Deleted, real.Deleted)
	assert.Equal(t, skippedIDs(dry), skippedIDs(real))
	assert.Equal(t, 1, countFolders(t, pool))
}

func skippedIDs(result *BatchDeleteResult) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(result.Skipped))
	for _, skipped := range result.Skipped {
		ids = append(ids, skipped.ID)
	}
	return ids
}

func countFolders(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(
		context.Background(), `SELECT count(*) FROM folders`).Scan(&count))
	return count
}

func seedFolderPack(t *testing.T, pool *pgxpool.Pool, ownerID, folderID uuid.UUID) uuid.UUID {
	t.Helper()
	packID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO packs (id, org_id, owner_id, folder_id, title, config)
		SELECT $1, u.org_id, u.id, $3, 'Pack', '{}'::jsonb
		FROM users u WHERE u.id = $2`, packID, ownerID, folderID)
	require.NoError(t, err)
	return packID
}
