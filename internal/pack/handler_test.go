package pack

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerCreatePack(t *testing.T) {
	folderID := uuid.New()
	service := &fakePackService{}
	service.createFn = func(_ context.Context, title string, gotFolderID uuid.UUID) (*Pack, error) {
		assert.Equal(t, "New pack", title)
		assert.Equal(t, folderID, gotFolderID)
		return &Pack{ID: uuid.New(), Title: title, FolderID: folderID}, nil
	}
	handler := NewHandler(service)
	body := []byte(`{"title":"New pack","folder_id":"` + folderID.String() + `"}`)

	rec := performPackRequest(t, handler.CreatePack, http.MethodPost, "/api/v1/packs", body, "")

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.True(t, service.createCalled)
	var result Pack
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.Equal(t, "New pack", result.Title)
}

func TestHandlerGetPack(t *testing.T) {
	service := &fakePackService{}
	packID := uuid.New()
	service.getFn = func(_ context.Context, gotPackID uuid.UUID) (*Pack, error) {
		assert.Equal(t, packID, gotPackID)
		return &Pack{ID: packID, Title: "Pack"}, nil
	}
	handler := NewHandler(service)

	rec := performPackRequest(t, handler.GetPack, http.MethodGet, "/api/v1/packs/"+packID.String(), nil, packID.String())

	assert.Equal(t, http.StatusOK, rec.Code)
	var result Pack
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.Equal(t, packID, result.ID)
}

func TestHandlerListPacks(t *testing.T) {
	service := &fakePackService{}
	folderID := uuid.New()
	packID := uuid.New()
	age := 5
	service.listFn = func(_ context.Context, input ListInput) ([]*ListItem, error) {
		assert.Equal(t, ListInput{
			Query: "speech", Age: &age, Difficulty: "medium",
			Section: "students", Limit: 25, Offset: 10,
		}, input)
		return []*ListItem{{
			ID: packID, FolderID: folderID, IsFavorite: true, Section: "students",
		}}, nil
	}
	handler := NewHandler(service)

	rec := performPackRequest(t, handler.ListPacks, http.MethodGet,
		"/api/v1/packs?query=speech&age=5&difficulty=medium&section=students&limit=25&offset=10",
		nil, "")

	assert.Equal(t, http.StatusOK, rec.Code)
	var result []*ListItem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, packID, result[0].ID)
	assert.True(t, result[0].IsFavorite)
	assert.Equal(t, folderID, result[0].FolderID)
	assert.Equal(t, "students", result[0].Section)
}

func TestHandlerUpdateRejectsConfigField(t *testing.T) {
	service := &fakePackService{}
	handler := NewHandler(service)
	packID := uuid.New()

	rec := performPackRequest(t, handler.UpdatePack, http.MethodPatch, "/api/v1/packs/"+packID.String(), []byte(`{"config":{}}`), packID.String())

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.False(t, service.updateCalled)
}

func TestHandlerUpdateMapsFilterMetadata(t *testing.T) {
	service := &fakePackService{}
	packID := uuid.New()
	service.updateFn = func(_ context.Context, gotPackID uuid.UUID, input UpdateInput) (*Pack, error) {
		assert.Equal(t, packID, gotPackID)
		require.NotNil(t, input.FilterMetadata)
		assert.True(t, input.FilterMetadata.AgeMin.Set)
		require.NotNil(t, input.FilterMetadata.AgeMin.Value)
		require.NotNil(t, input.FilterMetadata.Goals)
		assert.Equal(t, 5, *input.FilterMetadata.AgeMin.Value)
		assert.Equal(t, []string{"speech", "attention"}, *input.FilterMetadata.Goals)
		return &Pack{ID: packID}, nil
	}
	handler := NewHandler(service)
	body := []byte(`{"age_min":5,"goals":["speech","attention"]}`)

	rec := performPackRequest(t, handler.UpdatePack, http.MethodPatch, "/api/v1/packs/"+packID.String(), body, packID.String())

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, service.updateCalled)
}

func TestHandlerUpdatePreservesExplicitNull(t *testing.T) {
	service := &fakePackService{}
	packID := uuid.New()
	service.updateFn = func(_ context.Context, _ uuid.UUID, input UpdateInput) (*Pack, error) {
		require.NotNil(t, input.FilterMetadata)
		assert.True(t, input.FilterMetadata.AgeMin.Set)
		assert.Nil(t, input.FilterMetadata.AgeMin.Value)
		assert.True(t, input.FilterMetadata.Difficulty.Set)
		assert.Nil(t, input.FilterMetadata.Difficulty.Value)
		assert.True(t, input.Notes.Set)
		assert.Nil(t, input.Notes.Value)
		return &Pack{ID: packID}, nil
	}
	handler := NewHandler(service)
	body := []byte(`{"age_min":null,"difficulty":null,"notes":null}`)

	rec := performPackRequest(t, handler.UpdatePack, http.MethodPatch, "/api/v1/packs/"+packID.String(), body, packID.String())

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, service.updateCalled)
}

func TestHandlerListAllowsEmptyFilters(t *testing.T) {
	service := &fakePackService{}
	service.listFn = func(_ context.Context, input ListInput) ([]*ListItem, error) {
		assert.Equal(t, ListInput{Limit: 50}, input)
		return []*ListItem{{FolderID: uuid.New(), Section: "my"}}, nil
	}
	handler := NewHandler(service)

	rec := performPackRequest(t, handler.ListPacks, http.MethodGet, "/api/v1/packs", nil, "")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"is_favorite":false`)
	assert.Contains(t, rec.Body.String(), `"folder_id":`)
	assert.Contains(t, rec.Body.String(), `"section":"my"`)
}

func TestHandlerListRejectsInvalidPagination(t *testing.T) {
	tests := []string{"0", "101", "invalid"}

	for _, limit := range tests {
		t.Run(limit, func(t *testing.T) {
			handler := NewHandler(&fakePackService{})
			rec := performPackRequest(
				t,
				handler.ListPacks,
				http.MethodGet,
				"/api/v1/packs?limit="+limit,
				nil,
				"",
			)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestHandlerListRejectsInvalidAge(t *testing.T) {
	handler := NewHandler(&fakePackService{})

	rec := performPackRequest(t, handler.ListPacks, http.MethodGet, "/api/v1/packs?age=invalid", nil, "")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandlerDeletePack(t *testing.T) {
	service := &fakePackService{}
	packID := uuid.New()
	handler := NewHandler(service)

	rec := performPackRequest(t, handler.DeletePack, http.MethodDelete, "/api/v1/packs/"+packID.String(), nil, packID.String())

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, packID, service.deletedPackID)
}

func TestHandlerMovePack(t *testing.T) {
	service := &fakePackService{}
	packID := uuid.New()
	folderID := uuid.New()
	service.moveFn = func(_ context.Context, gotPackID, gotFolderID uuid.UUID) (*Pack, error) {
		assert.Equal(t, packID, gotPackID)
		assert.Equal(t, folderID, gotFolderID)
		return &Pack{ID: packID, FolderID: folderID}, nil
	}
	handler := NewHandler(service)
	body := []byte(`{"folder_id":"` + folderID.String() + `"}`)

	rec := performPackRequest(t, handler.MovePack, http.MethodPost, "/api/v1/packs/"+packID.String()+"/move", body, packID.String())

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandlerDuplicateAcceptsOptionalBody(t *testing.T) {
	packID, folderID := uuid.New(), uuid.New()
	tests := []struct {
		name       string
		body       []byte
		wantFolder *uuid.UUID
	}{
		{name: "empty body"},
		{name: "empty object", body: []byte(`{}`)},
		{name: "null folder", body: []byte(`{"folder_id":null}`)},
		{name: "folder", body: []byte(`{"folder_id":"` + folderID.String() + `"}`), wantFolder: &folderID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakePackService{}
			service.duplicateFn = func(
				_ context.Context,
				gotPackID uuid.UUID,
				input DuplicateInput,
			) (*Pack, error) {
				assert.Equal(t, packID, gotPackID)
				assert.Equal(t, test.wantFolder, input.FolderID)
				return &Pack{ID: uuid.New(), FolderID: folderID}, nil
			}
			rec := performPackRequest(
				t,
				NewHandler(service).DuplicatePack,
				http.MethodPost,
				"/api/v1/packs/"+packID.String()+"/duplicate",
				test.body,
				packID.String(),
			)
			assert.Equal(t, http.StatusCreated, rec.Code)
		})
	}
}

func TestHandlerDuplicateRejectsInvalidRequest(t *testing.T) {
	packID := uuid.New()
	tests := []struct {
		name   string
		pathID string
		body   []byte
	}{
		{name: "invalid pack id", pathID: "invalid", body: []byte(`{}`)},
		{name: "malformed json", pathID: packID.String(), body: []byte(`{`)},
		{name: "unknown field", pathID: packID.String(), body: []byte(`{"other":true}`)},
		{name: "invalid folder", pathID: packID.String(), body: []byte(`{"folder_id":"invalid"}`)},
		{name: "nil folder", pathID: packID.String(), body: []byte(`{"folder_id":"00000000-0000-0000-0000-000000000000"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			service := &fakePackService{}
			service.duplicateFn = func(
				context.Context, uuid.UUID, DuplicateInput,
			) (*Pack, error) {
				called = true
				return &Pack{}, nil
			}
			rec := performPackRequest(
				t,
				NewHandler(service).DuplicatePack,
				http.MethodPost,
				"/api/v1/packs/"+test.pathID+"/duplicate",
				test.body,
				test.pathID,
			)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.False(t, called)
		})
	}
}

func performPackRequest(
	t *testing.T,
	handler middleware.AppHandler,
	method, target string,
	body []byte,
	pathID string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, target, bytes.NewReader(body))
	if pathID != "" {
		req.SetPathValue("id", pathID)
	}
	rec := httptest.NewRecorder()
	middleware.ErrorMiddleware(handler).ServeHTTP(rec, req)
	return rec
}

type fakePackService struct {
	createCalled  bool
	updateCalled  bool
	deletedPackID uuid.UUID
	createFn      func(context.Context, string, uuid.UUID) (*Pack, error)
	duplicateFn   func(context.Context, uuid.UUID, DuplicateInput) (*Pack, error)
	getFn         func(context.Context, uuid.UUID) (*Pack, error)
	listFn        func(context.Context, ListInput) ([]*ListItem, error)
	updateFn      func(context.Context, uuid.UUID, UpdateInput) (*Pack, error)
	deleteFn      func(context.Context, uuid.UUID) error
	moveFn        func(context.Context, uuid.UUID, uuid.UUID) (*Pack, error)
}

func (f *fakePackService) Create(ctx context.Context, title string, folderID uuid.UUID) (*Pack, error) {
	f.createCalled = true
	if f.createFn != nil {
		return f.createFn(ctx, title, folderID)
	}
	return &Pack{}, nil
}

func (f *fakePackService) Duplicate(
	ctx context.Context,
	packID uuid.UUID,
	input DuplicateInput,
) (*Pack, error) {
	if f.duplicateFn != nil {
		return f.duplicateFn(ctx, packID, input)
	}
	return &Pack{}, nil
}
func (f *fakePackService) Get(ctx context.Context, packID uuid.UUID) (*Pack, error) {
	if f.getFn != nil {
		return f.getFn(ctx, packID)
	}
	return &Pack{}, nil
}

func (f *fakePackService) List(ctx context.Context, input ListInput) ([]*ListItem, error) {
	if f.listFn != nil {
		return f.listFn(ctx, input)
	}
	return []*ListItem{}, nil
}

func (f *fakePackService) Update(ctx context.Context, packID uuid.UUID, input UpdateInput) (*Pack, error) {
	f.updateCalled = true
	if f.updateFn != nil {
		return f.updateFn(ctx, packID, input)
	}
	return &Pack{}, nil
}

func (f *fakePackService) Delete(ctx context.Context, packID uuid.UUID) error {
	f.deletedPackID = packID
	if f.deleteFn != nil {
		return f.deleteFn(ctx, packID)
	}
	return nil
}

func (f *fakePackService) Move(ctx context.Context, packID, folderID uuid.UUID) (*Pack, error) {
	if f.moveFn != nil {
		return f.moveFn(ctx, packID, folderID)
	}
	return &Pack{}, nil
}

func (f *fakePackService) Publish(context.Context, uuid.UUID, uuid.UUID) (*Pack, error) {
	return &Pack{}, nil
}

func (f *fakePackService) Unpublish(context.Context, uuid.UUID) error {
	return nil
}
