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
	outcome *BatchOutcome
	gotIDs  []uuid.UUID
}

func (s *stubRepository) UserOrg(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (s *stubRepository) Upsert(context.Context, File) (*File, error) { return nil, nil }

func (s *stubRepository) GetAccessible(context.Context, uuid.UUID, uuid.UUID) (*File, error) {
	return nil, nil
}

func (s *stubRepository) List(
	context.Context, uuid.UUID, string, string, *mediaCursor, bool, int,
) ([]File, error) {
	return nil, nil
}

func (s *stubRepository) Count(context.Context, uuid.UUID, string, string, bool) (int, error) {
	return 0, nil
}

func (s *stubRepository) Delete(context.Context, uuid.UUID, uuid.UUID) (*File, error) {
	return nil, nil
}

func (s *stubRepository) DeleteBatch(
	_ context.Context, _ uuid.UUID, ids []uuid.UUID,
) (*BatchOutcome, error) {
	s.gotIDs = ids
	return s.outcome, nil
}

func TestServiceDeleteBatchRejectsEmptyAndOversizedBatch(t *testing.T) {
	repo := &stubRepository{}
	service := NewService(repo, nil)
	ctx := authctx.SetUserIDToCtx(t.Context(), uuid.New())

	var appErr *apperr.AppError
	_, err := service.DeleteBatch(ctx, nil)
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPStatus)

	ids := make([]uuid.UUID, MaxBatchDeleteIDs+1)
	for i := range ids {
		ids[i] = uuid.New()
	}
	_, err = service.DeleteBatch(ctx, ids)
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPStatus)
	assert.Nil(t, repo.gotIDs, "пачка сверх лимита до репозитория не доходит")
}

func TestServiceDeleteBatchReportsSkippedWithReason(t *testing.T) {
	deleted, used, missing := uuid.New(), uuid.New(), uuid.New()
	repo := &stubRepository{outcome: &BatchOutcome{
		Deleted: []uuid.UUID{deleted},
		InUse:   []uuid.UUID{used},
	}}
	service := NewService(repo, nil)
	ctx := authctx.SetUserIDToCtx(t.Context(), uuid.New())

	result, err := service.DeleteBatch(ctx, []uuid.UUID{deleted, deleted, used, missing})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{deleted, used, missing}, repo.gotIDs, "повторы схлопываются")
	assert.Equal(t, []uuid.UUID{deleted}, result.Deleted)

	// Всё, что не удалено, возвращается с причиной отказа.
	assert.Equal(t, []SkippedMedia{
		{ID: used, Reason: SkipReasonInUse},
		{ID: missing, Reason: SkipReasonNotFound},
	}, result.Skipped)
}
