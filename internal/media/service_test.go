package media

import (
	"context"
	"net/http"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectMIMEAcceptsGIF(t *testing.T) {
	data := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00")
	assert.Equal(t, "image/gif", detectMIME(data))
}

type stubRepository struct {
	outcome   *BatchOutcome
	gotIDs    []uuid.UUID
	gotDryRun bool
	called    bool
}

func (s *stubRepository) UserOrg(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (s *stubRepository) Upsert(context.Context, File) (*File, error) { return nil, nil }

func (s *stubRepository) GetAccessible(context.Context, uuid.UUID, uuid.UUID) (*File, error) {
	return nil, nil
}

func (s *stubRepository) ListWithTotal(context.Context, ListQuery) ([]ListItem, int, error) {
	return nil, 0, nil
}

func (s *stubRepository) Delete(context.Context, uuid.UUID, uuid.UUID) (*File, error) {
	return nil, nil
}

func (s *stubRepository) DeleteBatch(
	_ context.Context, _ uuid.UUID, ids []uuid.UUID, dryRun bool,
) (*BatchOutcome, error) {
	s.called = true
	s.gotIDs = ids
	s.gotDryRun = dryRun
	return s.outcome, nil
}

func TestServiceDeleteBatchRejectsEmptyAndOversizedBatch(t *testing.T) {
	repo := &stubRepository{}
	service := NewService(repo, nil, 0)
	ctx := authctx.SetUserIDToCtx(t.Context(), uuid.New())

	// Без лимита в конфиге сервис берёт значение по умолчанию.
	require.Equal(t, DefaultBatchDeleteLimit, service.batchDeleteLimit)

	var appErr *apperr.AppError
	_, err := service.DeleteBatch(ctx, nil, false)
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPStatus)

	ids := make([]uuid.UUID, DefaultBatchDeleteLimit+1)
	for i := range ids {
		ids[i] = uuid.New()
	}
	_, err = service.DeleteBatch(ctx, ids, false)
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPStatus)
	assert.False(t, repo.called, "пачка сверх лимита до репозитория не доходит")
}

func TestServiceDeleteBatchHonoursConfiguredLimit(t *testing.T) {
	repo := &stubRepository{outcome: &BatchOutcome{}}
	service := NewService(repo, nil, 2)
	ctx := authctx.SetUserIDToCtx(t.Context(), uuid.New())

	_, err := service.DeleteBatch(ctx, []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}, false)
	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPStatus)
	assert.Contains(t, appErr.Message, "at most 2")
}

func TestServiceDeleteBatchCollapsesDuplicateIDs(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	repo := &stubRepository{outcome: &BatchOutcome{Deleted: []uuid.UUID{first, second}}}
	service := NewService(repo, nil, DefaultBatchDeleteLimit)
	ctx := authctx.SetUserIDToCtx(t.Context(), uuid.New())

	result, err := service.DeleteBatch(ctx, []uuid.UUID{first, second, first, first, second}, false)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{first, second}, repo.gotIDs, "в репозиторий уходят только уникальные id")
	assert.Equal(t, []uuid.UUID{first, second}, result.Deleted)
	assert.Empty(t, result.Skipped, "дубликат не превращается в отказ not_found")
}

func TestServiceDeleteBatchReportsSkippedWithReason(t *testing.T) {
	deleted, used, missing := uuid.New(), uuid.New(), uuid.New()
	repo := &stubRepository{outcome: &BatchOutcome{
		Deleted:    []uuid.UUID{deleted},
		InUse:      []uuid.UUID{used},
		FreedBytes: 512,
	}}
	service := NewService(repo, nil, DefaultBatchDeleteLimit)
	ctx := authctx.SetUserIDToCtx(t.Context(), uuid.New())

	result, err := service.DeleteBatch(ctx, []uuid.UUID{deleted, used, missing}, false)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{deleted}, result.Deleted)
	assert.Equal(t, int64(512), result.FreedBytes)
	assert.False(t, result.DryRun)

	// Всё, что не удалено, возвращается с причиной отказа.
	assert.Equal(t, []SkippedMedia{
		{ID: used, Reason: SkipReasonInUse},
		{ID: missing, Reason: SkipReasonNotFound},
	}, result.Skipped)
}

func TestServiceDeleteBatchPassesDryRunThrough(t *testing.T) {
	repo := &stubRepository{outcome: &BatchOutcome{FreedBytes: 128}}
	service := NewService(repo, nil, DefaultBatchDeleteLimit)
	ctx := authctx.SetUserIDToCtx(t.Context(), uuid.New())

	result, err := service.DeleteBatch(ctx, []uuid.UUID{uuid.New()}, true)
	require.NoError(t, err)
	assert.True(t, repo.gotDryRun)
	assert.True(t, result.DryRun)
	assert.Equal(t, int64(128), result.FreedBytes, "dry-run показывает, сколько освободится")
}
