package pack

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentServiceExportValidatesConfigBeforeArchive(t *testing.T) {
	repo := &exportContentRepository{
		pack: &Pack{
			ID: uuid.New(), Title: "Invalid",
			Config: json.RawMessage(`{
				"metadata":{"version":"2.0"},
				"settings":{"columns":1,"rows":101},
				"blocks":[]
			}`),
		},
	}
	service := NewContentService(repo, nil, nil, nil)

	_, err := service.Export(packContext(uuid.New()), repo.pack.ID)

	assertAppErrorStatus(t, err, http.StatusBadRequest)
}

func TestContentServiceExportReturnsConflictForMissingMedia(t *testing.T) {
	mediaID := uuid.New()
	repo := &exportContentRepository{
		pack: &Pack{
			ID: mediaID, Title: "Missing media", Config: archiveConfigWithMediaID(mediaID),
		},
	}
	service := NewContentService(repo, fakeArchiveStorage{}, nil, nil)

	_, err := service.Export(packContext(uuid.New()), repo.pack.ID)

	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusConflict, appErr.HTTPStatus)
}

func TestContentServiceExportReturnsConflictForMissingPicture(t *testing.T) {
	pictureID := uuid.New()
	repo := &exportContentRepository{
		pack: &Pack{
			ID: uuid.New(), Title: "Missing picture",
			Config: json.RawMessage(`{
				"metadata":{"version":"2.0"},
				"settings":{"columns":1,"rows":1},
				"blocks":[{"id":"b","type":"grid","elements":[{
					"id":"e","kind":"image","source_picture_id":"` + pictureID.String() + `"
				}]}]
			}`),
		},
	}
	service := NewContentService(
		repo, fakeArchiveStorage{}, nil, nil,
		func(context.Context, uuid.UUID) ([]byte, string, error) {
			return nil, "", ErrMissingMediaReference
		},
	)

	_, err := service.Export(packContext(uuid.New()), repo.pack.ID)

	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusConflict, appErr.HTTPStatus)
}

func TestContentServiceExportAdaptationUsesSnapshotAndSafeFilename(t *testing.T) {
	mediaID := uuid.New()
	repo := &exportContentRepository{
		adaptation: &adaptationArchiveData{
			Title: " Lesson/One\n", Config: archiveConfigWithMediaID(mediaID),
		},
		adaptationFiles: []*media.File{{
			ID: mediaID, MIMEType: "image/png", SizeBytes: 3, MinIOKey: "adaptation-object",
		}},
	}
	service := NewContentService(
		repo,
		fakeArchiveStorage{objects: map[string][]byte{"adaptation-object": {1, 2, 3}}},
		nil,
		nil,
	)

	archive, err := service.ExportAdaptation(packContext(uuid.New()), uuid.New())

	require.NoError(t, err)
	require.NotNil(t, archive)
	assert.Equal(t, "Lesson_One-adaptation.linka", archive.Filename)
	assert.Greater(t, archive.Size, int64(0))
	require.NoError(t, archive.Stream.Close())
}

func TestContentServiceExportAdaptationReturnsNotFound(t *testing.T) {
	repo := &exportContentRepository{err: ErrAdaptationNotFound}
	service := NewContentService(repo, nil, nil, nil)

	_, err := service.ExportAdaptation(packContext(uuid.New()), uuid.New())

	assertAppErrorStatus(t, err, http.StatusNotFound)
}

func TestContentServiceExportAdaptationReturnsConflictForMissingMedia(t *testing.T) {
	mediaID := uuid.New()
	repo := &exportContentRepository{
		adaptation: &adaptationArchiveData{
			Title: "Adaptation", Config: archiveConfigWithMediaID(mediaID),
		},
	}
	service := NewContentService(repo, fakeArchiveStorage{}, nil, nil)

	_, err := service.ExportAdaptation(packContext(uuid.New()), uuid.New())

	assertAppErrorStatus(t, err, http.StatusConflict)
}

func TestContentServiceExportAdaptationValidatesConfig(t *testing.T) {
	repo := &exportContentRepository{
		adaptation: &adaptationArchiveData{
			Title: "Invalid",
			Config: json.RawMessage(`{
				"metadata":{"version":"2.0"},
				"settings":{"columns":1,"rows":101},
				"blocks":[]
			}`),
		},
	}
	service := NewContentService(repo, nil, nil, nil)

	_, err := service.ExportAdaptation(packContext(uuid.New()), uuid.New())

	assertAppErrorStatus(t, err, http.StatusBadRequest)
}

func TestContentServiceExportReturnsStreamAndSafeFilename(t *testing.T) {
	mediaID := uuid.New()
	repo := &exportContentRepository{
		pack: &Pack{
			ID: uuid.New(), Title: " Lesson/One\n", Config: archiveConfigWithMediaID(mediaID),
		},
		files: []*media.File{{
			ID: mediaID, MIMEType: "image/png", SizeBytes: 3, MinIOKey: "object",
		}},
	}
	service := NewContentService(
		repo, fakeArchiveStorage{objects: map[string][]byte{"object": {1, 2, 3}}}, nil, nil,
	)

	archive, err := service.Export(packContext(uuid.New()), repo.pack.ID)

	require.NoError(t, err)
	require.NotNil(t, archive)
	assert.Equal(t, "Lesson_One.linka", archive.Filename)
	assert.Greater(t, archive.Size, int64(0))
	require.NoError(t, archive.Stream.Close())
}

type exportContentRepository struct {
	pack            *Pack
	files           []*media.File
	adaptation      *adaptationArchiveData
	adaptationFiles []*media.File
	err             error
}

func (r *exportContentRepository) SaveConfig(
	context.Context, uuid.UUID, uuid.UUID, json.RawMessage, []uuid.UUID,
) (*Pack, error) {
	return nil, nil
}

func (r *exportContentRepository) ArchiveData(
	context.Context, uuid.UUID, uuid.UUID,
) (*Pack, []*media.File, error) {
	return r.pack, r.files, r.err
}

func (r *exportContentRepository) AdaptationArchiveData(
	context.Context, uuid.UUID, uuid.UUID,
) (*adaptationArchiveData, []*media.File, error) {
	return r.adaptation, r.adaptationFiles, r.err
}

func (r *exportContentRepository) Assign(
	context.Context, uuid.UUID, uuid.UUID, []uuid.UUID,
) ([]Adaptation, error) {
	return nil, nil
}

func (r *exportContentRepository) Unassign(
	context.Context, uuid.UUID, uuid.UUID, uuid.UUID,
) error {
	return nil
}

func (r *exportContentRepository) CreateVersion(
	context.Context, uuid.UUID, uuid.UUID,
) (*Version, error) {
	return nil, nil
}

func (r *exportContentRepository) ListVersions(
	context.Context, uuid.UUID, uuid.UUID, ListInput,
) ([]*VersionSummary, error) {
	return nil, nil
}

func (r *exportContentRepository) GetVersion(
	context.Context, uuid.UUID, uuid.UUID, int,
) (*Version, error) {
	return nil, nil
}

func (r *exportContentRepository) RestoreVersion(
	context.Context, uuid.UUID, uuid.UUID, int,
) (*RestoreResult, error) {
	return nil, nil
}
