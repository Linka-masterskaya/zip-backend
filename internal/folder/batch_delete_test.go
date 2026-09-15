package folder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/bulk"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeFolderRepository struct {
	deleteBatchFn func(context.Context, uuid.UUID, string, []uuid.UUID, bool) (*BatchOutcome, error)
}

func (f *fakeFolderRepository) Create(
	context.Context, uuid.UUID, string, CreateInput,
) (*Folder, error) {
	return &Folder{}, nil
}

func (f *fakeFolderRepository) List(context.Context, uuid.UUID, ListInput) ([]Folder, error) {
	return nil, nil
}

func (f *fakeFolderRepository) Rename(
	context.Context, uuid.UUID, string, uuid.UUID, string,
) (*Folder, error) {
	return &Folder{}, nil
}

func (f *fakeFolderRepository) Move(
	context.Context, uuid.UUID, string, uuid.UUID, *uuid.UUID,
) (*Folder, error) {
	return &Folder{}, nil
}

func (f *fakeFolderRepository) Delete(context.Context, uuid.UUID, string, uuid.UUID) error {
	return nil
}

func (f *fakeFolderRepository) DeleteBatch(
	ctx context.Context,
	userID uuid.UUID,
	role string,
	ids []uuid.UUID,
	dryRun bool,
) (*BatchOutcome, error) {
	if f.deleteBatchFn != nil {
		return f.deleteBatchFn(ctx, userID, role, ids, dryRun)
	}
	return &BatchOutcome{Deleted: ids, NotEmpty: []uuid.UUID{}}, nil
}

func (f *fakeFolderRepository) Contents(
	context.Context, uuid.UUID, ContentsInput,
) (*ContentsPage, error) {
	return &ContentsPage{}, nil
}

type fakeFolderService struct {
	deleteBatchFn func(context.Context, []uuid.UUID, bool) (*BatchDeleteResult, error)
}

func (f *fakeFolderService) Create(context.Context, CreateInput) (*Folder, error) {
	return &Folder{}, nil
}

func (f *fakeFolderService) List(context.Context, ListInput) ([]Folder, error) {
	return nil, nil
}

func (f *fakeFolderService) Rename(context.Context, uuid.UUID, string) (*Folder, error) {
	return &Folder{}, nil
}

func (f *fakeFolderService) Move(context.Context, uuid.UUID, *uuid.UUID) (*Folder, error) {
	return &Folder{}, nil
}

func (f *fakeFolderService) Delete(context.Context, uuid.UUID) error {
	return nil
}

func (f *fakeFolderService) DeleteBatch(
	ctx context.Context,
	ids []uuid.UUID,
	dryRun bool,
) (*BatchDeleteResult, error) {
	if f.deleteBatchFn != nil {
		return f.deleteBatchFn(ctx, ids, dryRun)
	}
	return &BatchDeleteResult{Deleted: ids, Skipped: []bulk.Skipped{}, DryRun: dryRun}, nil
}

func (f *fakeFolderService) Contents(context.Context, ContentsInput) (*ContentsPage, error) {
	return &ContentsPage{}, nil
}

func TestServiceDeleteBatchRejectsEmptyAndOversizedInput(t *testing.T) {
	service := NewService(&fakeFolderRepository{}, 2)
	ctx := folderContext(uuid.New())

	_, err := service.DeleteBatch(ctx, nil, false)
	assertStatus(t, err, apperr.ErrBadRequest.HTTPStatus)

	_, err = service.DeleteBatch(ctx, []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}, false)
	assertStatus(t, err, apperr.ErrBadRequest.HTTPStatus)
}

func TestServiceDeleteBatchReportsSkipReasons(t *testing.T) {
	deletedID := uuid.New()
	notEmptyID := uuid.New()
	missingID := uuid.New()
	repo := &fakeFolderRepository{}
	repo.deleteBatchFn = func(
		_ context.Context, _ uuid.UUID, role string, ids []uuid.UUID, dryRun bool,
	) (*BatchOutcome, error) {
		assert.Equal(t, "defectologist", role)
		assert.True(t, dryRun, "dry_run must reach the repository")
		assert.Equal(t, []uuid.UUID{deletedID, notEmptyID, missingID}, ids)
		return &BatchOutcome{
			Deleted:  []uuid.UUID{deletedID},
			NotEmpty: []uuid.UUID{notEmptyID},
		}, nil
	}

	result, err := NewService(repo, 0).DeleteBatch(
		folderContext(uuid.New()),
		[]uuid.UUID{deletedID, notEmptyID, missingID, deletedID},
		true,
	)
	require.NoError(t, err)
	assert.True(t, result.DryRun)
	assert.Equal(t, []uuid.UUID{deletedID}, result.Deleted)
	assert.ElementsMatch(t, []bulk.Skipped{
		{ID: notEmptyID, Reason: bulk.ReasonNotEmpty},
		{ID: missingID, Reason: bulk.ReasonNotFound},
	}, result.Skipped)
}

func TestHandlerBatchDeleteFolders(t *testing.T) {
	folderID := uuid.New()
	service := &fakeFolderService{}
	service.deleteBatchFn = func(
		_ context.Context, ids []uuid.UUID, dryRun bool,
	) (*BatchDeleteResult, error) {
		assert.Equal(t, []uuid.UUID{folderID}, ids)
		assert.False(t, dryRun)
		return &BatchDeleteResult{
			Deleted: []uuid.UUID{},
			Skipped: []bulk.Skipped{{ID: folderID, Reason: bulk.ReasonNotEmpty}},
		}, nil
	}
	handler := NewHandler(service)
	body := `{"ids":["` + folderID.String() + `"]}`

	rec := performFolderRequest(t, handler.BatchDelete, body)

	assert.Equal(t, http.StatusOK, rec.Code)
	var result BatchDeleteResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.Equal(t, []bulk.Skipped{{ID: folderID, Reason: bulk.ReasonNotEmpty}}, result.Skipped)
}

func TestHandlerBatchDeleteFoldersRejectsUnknownFields(t *testing.T) {
	handler := NewHandler(&fakeFolderService{})

	rec := performFolderRequest(t, handler.BatchDelete, `{"ids":[],"force":true}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func performFolderRequest(
	t *testing.T,
	handler middleware.AppHandler,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "/api/v1/folders/batch-delete",
		strings.NewReader(body),
	)
	rec := httptest.NewRecorder()
	middleware.ErrorMiddleware(handler).ServeHTTP(rec, req)
	return rec
}
