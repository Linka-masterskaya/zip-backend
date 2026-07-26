package pack

import (
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

func TestContentHandlerVersionLifecycle(t *testing.T) {
	packID := uuid.New()
	service := &fakeContentVersionService{
		createVersionFn: func(_ context.Context, gotID uuid.UUID) (*Version, error) {
			assert.Equal(t, packID, gotID)
			return &Version{ID: uuid.New(), PackID: gotID, Version: 1}, nil
		},
		listVersionsFn: func(
			_ context.Context,
			gotID uuid.UUID,
			input ListInput,
		) ([]*VersionSummary, error) {
			assert.Equal(t, packID, gotID)
			assert.Equal(t, ListInput{Limit: 25, Offset: 2}, input)
			return []*VersionSummary{{PackID: gotID, Version: 1}}, nil
		},
		getVersionFn: func(
			_ context.Context,
			gotID uuid.UUID,
			version int,
		) (*Version, error) {
			assert.Equal(t, packID, gotID)
			assert.Equal(t, 1, version)
			return &Version{PackID: gotID, Version: version}, nil
		},
		restoreVersionFn: func(
			_ context.Context,
			gotID uuid.UUID,
			version int,
		) (*RestoreResult, error) {
			assert.Equal(t, packID, gotID)
			assert.Equal(t, 1, version)
			return &RestoreResult{
				Pack:                &Pack{ID: gotID},
				RestoredFromVersion: version,
				BackupVersion:       &Version{PackID: gotID, Version: 2},
			}, nil
		},
	}
	handler := NewContentHandler(service)

	created := performContentVersionRequest(
		t, handler.CreateVersion, http.MethodPost,
		"/api/v1/packs/"+packID.String()+"/versions", packID, "",
	)
	assert.Equal(t, http.StatusCreated, created.Code)

	listed := performContentVersionRequest(
		t, handler.ListVersions, http.MethodGet,
		"/api/v1/packs/"+packID.String()+"/versions?limit=25&offset=2", packID, "",
	)
	assert.Equal(t, http.StatusOK, listed.Code)
	var versions []*VersionSummary
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &versions))
	require.Len(t, versions, 1)

	fetched := performContentVersionRequest(
		t, handler.GetVersion, http.MethodGet,
		"/api/v1/packs/"+packID.String()+"/versions/1", packID, "1",
	)
	assert.Equal(t, http.StatusOK, fetched.Code)

	restored := performContentVersionRequest(
		t, handler.RestoreVersion, http.MethodPost,
		"/api/v1/packs/"+packID.String()+"/versions/1/restore", packID, "1",
	)
	assert.Equal(t, http.StatusOK, restored.Code)
}

func TestContentHandlerRestoreRejectsInvalidVersion(t *testing.T) {
	handler := NewContentHandler(&fakeContentVersionService{})

	rec := performContentVersionRequest(
		t, handler.RestoreVersion, http.MethodPost,
		"/api/v1/packs/id/versions/zero/restore", uuid.New(), "zero",
	)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func performContentVersionRequest(
	t *testing.T,
	handler middleware.AppHandler,
	method, target string,
	packID uuid.UUID,
	version string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, target, nil)
	req.SetPathValue("id", packID.String())
	if version != "" {
		req.SetPathValue("version", version)
	}
	rec := httptest.NewRecorder()
	middleware.ErrorMiddleware(handler).ServeHTTP(rec, req)
	return rec
}

type fakeContentVersionService struct {
	createVersionFn  func(context.Context, uuid.UUID) (*Version, error)
	listVersionsFn   func(context.Context, uuid.UUID, ListInput) ([]*VersionSummary, error)
	getVersionFn     func(context.Context, uuid.UUID, int) (*Version, error)
	restoreVersionFn func(context.Context, uuid.UUID, int) (*RestoreResult, error)
}

func (f *fakeContentVersionService) SaveConfig(
	context.Context, uuid.UUID, json.RawMessage,
) (*Pack, error) {
	return &Pack{}, nil
}

func (f *fakeContentVersionService) Export(context.Context, uuid.UUID) ([]byte, string, error) {
	return nil, "", nil
}

func (f *fakeContentVersionService) Import(
	context.Context, string, uuid.UUID, []byte,
) (*Pack, error) {
	return &Pack{}, nil
}

func (f *fakeContentVersionService) Assign(
	context.Context, uuid.UUID, []uuid.UUID,
) ([]Adaptation, error) {
	return nil, nil
}

func (f *fakeContentVersionService) Unassign(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (f *fakeContentVersionService) CreateVersion(
	ctx context.Context, packID uuid.UUID,
) (*Version, error) {
	if f.createVersionFn != nil {
		return f.createVersionFn(ctx, packID)
	}
	return &Version{}, nil
}

func (f *fakeContentVersionService) ListVersions(
	ctx context.Context, packID uuid.UUID, input ListInput,
) ([]*VersionSummary, error) {
	if f.listVersionsFn != nil {
		return f.listVersionsFn(ctx, packID, input)
	}
	return []*VersionSummary{}, nil
}

func (f *fakeContentVersionService) GetVersion(
	ctx context.Context, packID uuid.UUID, version int,
) (*Version, error) {
	if f.getVersionFn != nil {
		return f.getVersionFn(ctx, packID, version)
	}
	return &Version{}, nil
}

func (f *fakeContentVersionService) RestoreVersion(
	ctx context.Context, packID uuid.UUID, version int,
) (*RestoreResult, error) {
	if f.restoreVersionFn != nil {
		return f.restoreVersionFn(ctx, packID, version)
	}
	return &RestoreResult{}, nil
}
