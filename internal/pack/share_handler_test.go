package pack

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestShareHandlerFolderReturnsCreatedPack(t *testing.T) {
	packID, folderID := uuid.New(), uuid.New()
	service := &shareHandlerServiceFake{shareFn: func(
		_ context.Context, gotPackID uuid.UUID, input ShareInput,
	) (*ShareResult, error) {
		assert.Equal(t, packID, gotPackID)
		assert.Equal(t, ShareTargetFolder, input.TargetType)
		assert.Equal(t, folderID, input.TargetID)
		return &ShareResult{Pack: &Pack{ID: uuid.New(), FolderID: folderID}}, nil
	}}

	rec := performShareRequest(t, NewShareHandler(service), packID,
		[]byte(`{"target_type":"folder","target_id":"`+folderID.String()+`"}`))

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), folderID.String())
}

func TestShareHandlerStudentReturnsAccepted(t *testing.T) {
	packID, studentID := uuid.New(), uuid.New()
	service := &shareHandlerServiceFake{shareFn: func(
		_ context.Context, _ uuid.UUID, input ShareInput,
	) (*ShareResult, error) {
		assert.Equal(t, ShareTargetStudent, input.TargetType)
		assert.Equal(t, studentID, input.TargetID)
		return &ShareResult{Task: &ShareTask{ID: uuid.New(), Status: ShareTaskQueued}}, nil
	}}

	rec := performShareRequest(t, NewShareHandler(service), packID,
		[]byte(`{"target_type":"student","target_id":"`+studentID.String()+`"}`))

	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"queued"`)
}

func TestShareHandlerGetTaskReturnsStatus(t *testing.T) {
	taskID := uuid.New()
	service := &shareHandlerServiceFake{taskFn: func(_ context.Context, gotTaskID uuid.UUID) (*ShareTask, error) {
		assert.Equal(t, taskID, gotTaskID)
		return &ShareTask{ID: taskID, Status: ShareTaskSent}, nil
	}}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/pack-share-tasks/"+taskID.String(), nil)
	req.SetPathValue("id", taskID.String())
	rec := httptest.NewRecorder()
	middleware.ErrorMiddleware(NewShareHandler(service).GetShareTask).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"sent"`)
}

func TestShareHandlerRejectsUnknownFieldsAndEmptyTarget(t *testing.T) {
	packID := uuid.New()
	for _, body := range [][]byte{
		[]byte(`{"target_type":"student","target_id":"00000000-0000-0000-0000-000000000000"}`),
		[]byte(`{"target_type":"student","target_id":"` + uuid.New().String() + `","pdf":true}`),
	} {
		rec := performShareRequest(t, NewShareHandler(&shareHandlerServiceFake{}), packID, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	}
}

func performShareRequest(t *testing.T, handler *ShareHandler, packID uuid.UUID, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/packs/"+packID.String()+"/share", bytes.NewReader(body))
	req.SetPathValue("id", packID.String())
	rec := httptest.NewRecorder()
	middleware.ErrorMiddleware(handler.SharePack).ServeHTTP(rec, req)
	return rec
}

type shareHandlerServiceFake struct {
	shareFn func(context.Context, uuid.UUID, ShareInput) (*ShareResult, error)
	taskFn  func(context.Context, uuid.UUID) (*ShareTask, error)
}

func (f *shareHandlerServiceFake) Share(ctx context.Context, packID uuid.UUID, input ShareInput) (*ShareResult, error) {
	if f.shareFn != nil {
		return f.shareFn(ctx, packID, input)
	}
	return &ShareResult{}, nil
}

func (f *shareHandlerServiceFake) GetTask(ctx context.Context, taskID uuid.UUID) (*ShareTask, error) {
	if f.taskFn != nil {
		return f.taskFn(ctx, taskID)
	}
	return &ShareTask{ID: taskID, Status: ShareTaskQueued}, nil
}
