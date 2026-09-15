package pack

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/bulk"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceDeleteBatchRejectsEmptyAndOversizedInput(t *testing.T) {
	service := NewService(&fakePackRepository{}, nil, 2)
	ctx := packContext(uuid.New())

	_, err := service.DeleteBatch(ctx, nil, false)
	assertAppErrorStatus(t, err, http.StatusBadRequest)

	_, err = service.DeleteBatch(ctx, []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}, false)
	assertAppErrorStatus(t, err, http.StatusBadRequest)
}

// Повторы схлопываются до обращения к базе: клиент мог прислать один и тот же
// набор дважды, а причина пропуска должна прийти по одному разу.
func TestServiceDeleteBatchCollapsesDuplicates(t *testing.T) {
	packID := uuid.New()
	repo := &fakePackRepository{}
	var requested []uuid.UUID
	repo.deleteBatchFn = func(
		_ context.Context, _ uuid.UUID, ids []uuid.UUID, _ bool,
	) (*BatchOutcome, error) {
		requested = ids
		return &BatchOutcome{Deleted: ids, Published: []uuid.UUID{}}, nil
	}

	result, err := NewService(repo, nil, 0).DeleteBatch(
		packContext(uuid.New()), []uuid.UUID{packID, packID, uuid.Nil}, false,
	)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{packID}, requested)
	assert.Equal(t, []uuid.UUID{packID}, result.Deleted)
	assert.Empty(t, result.Skipped)
}

func TestServiceDeleteBatchReportsSkipReasons(t *testing.T) {
	deletedID := uuid.New()
	publishedID := uuid.New()
	missingID := uuid.New()
	repo := &fakePackRepository{}
	repo.deleteBatchFn = func(
		_ context.Context, _ uuid.UUID, _ []uuid.UUID, dryRun bool,
	) (*BatchOutcome, error) {
		assert.True(t, dryRun, "dry_run must reach the repository")
		return &BatchOutcome{
			Deleted:   []uuid.UUID{deletedID},
			Published: []uuid.UUID{publishedID},
		}, nil
	}

	result, err := NewService(repo, nil, 0).DeleteBatch(
		packContext(uuid.New()), []uuid.UUID{deletedID, publishedID, missingID}, true,
	)
	require.NoError(t, err)
	assert.True(t, result.DryRun)
	assert.Equal(t, []uuid.UUID{deletedID}, result.Deleted)
	assert.Equal(t, []bulk.Skipped{
		{ID: publishedID, Reason: bulk.ReasonPublished},
		{ID: missingID, Reason: bulk.ReasonNotFound},
	}, result.Skipped)
}

func TestHandlerBatchDeletePacks(t *testing.T) {
	packID := uuid.New()
	service := &fakePackService{}
	service.deleteBatchFn = func(
		_ context.Context, ids []uuid.UUID, dryRun bool,
	) (*BatchDeleteResult, error) {
		assert.Equal(t, []uuid.UUID{packID}, ids)
		assert.True(t, dryRun)
		return &BatchDeleteResult{
			Deleted: []uuid.UUID{},
			Skipped: []bulk.Skipped{{ID: packID, Reason: bulk.ReasonPublished}},
			DryRun:  true,
		}, nil
	}
	handler := NewHandler(service)
	body := []byte(`{"ids":["` + packID.String() + `"],"dry_run":true}`)

	rec := performPackRequest(
		t, handler.BatchDeletePacks, http.MethodPost, "/api/v1/packs/batch-delete", body, "",
	)

	assert.Equal(t, http.StatusOK, rec.Code)
	var result BatchDeleteResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.True(t, result.DryRun)
	assert.Equal(t, []bulk.Skipped{{ID: packID, Reason: bulk.ReasonPublished}}, result.Skipped)
}

func TestHandlerBatchDeletePacksRejectsUnknownFields(t *testing.T) {
	handler := NewHandler(&fakePackService{})
	body := []byte(`{"ids":[],"force":true}`)

	rec := performPackRequest(
		t, handler.BatchDeletePacks, http.MethodPost, "/api/v1/packs/batch-delete", body, "",
	)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
