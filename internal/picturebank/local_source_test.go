package picturebank

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalSourceImplementsSourceContract(t *testing.T) {
	pictureID := uuid.New()
	image := testPNG()
	repo := &fakeLocalRepository{
		categories: []string{"Животные"},
		pictures: []localPictureMetadata{{
			ID: pictureID, Category: "Животные", Title: "Кот",
			MIMEType: "image/png", SizeBytes: int64(len(image)),
			MinIOKey: LocalObjectPrefix + "/" + pictureID.String(),
		}},
	}
	objectStorage := &fakeLocalStorage{objects: map[string][]byte{
		repo.pictures[0].MinIOKey: image,
	}}
	source, err := newLocalSource(repo, objectStorage, 1024*1024)
	require.NoError(t, err)

	categories, err := source.Categories(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []Category{{ID: "Животные", Name: "Животные"}}, categories)

	pictures, err := source.Search(t.Context(), "кот")
	require.NoError(t, err)
	require.Len(t, pictures, 1)
	assert.Equal(t, pictureID.String(), pictures[0].ID)
	assert.Equal(t, "Кот", pictures[0].Name)
	assert.Equal(t, "image/png", pictures[0].MIMEType)
	assert.Equal(t, categories, pictures[0].Categories)

	loaded, err := source.Image(t.Context(), pictureID.String())
	require.NoError(t, err)
	assert.Equal(t, image, loaded.Data)
	assert.Equal(t, "image/png", loaded.ContentType)
}

func TestLocalSourceMapsDeletedPicturesToSharedPlaceholderError(t *testing.T) {
	pictureID := uuid.New()
	tests := []struct {
		name    string
		repo    *fakeLocalRepository
		storage *fakeLocalStorage
	}{
		{
			name:    "metadata removed",
			repo:    &fakeLocalRepository{getErr: ErrPictureNotFound},
			storage: &fakeLocalStorage{},
		},
		{
			name: "object removed",
			repo: &fakeLocalRepository{get: &localPictureMetadata{
				ID: pictureID, Category: "Животные", Title: "Кот", MIMEType: "image/png",
				SizeBytes: 1, MinIOKey: LocalObjectPrefix + "/" + pictureID.String(),
			}},
			storage: &fakeLocalStorage{getErr: storage.ErrObjectNotFound},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := newLocalSource(test.repo, test.storage, 1024)
			require.NoError(t, err)

			_, err = source.Image(t.Context(), pictureID.String())

			require.ErrorIs(t, err, ErrPictureNotFound)
		})
	}
}

func TestLocalSourceMapsInfrastructureAndCorruptionErrorsLikeExternalAdapter(t *testing.T) {
	pictureID := uuid.New()
	t.Run("database unavailable", func(t *testing.T) {
		source, err := newLocalSource(
			&fakeLocalRepository{categoriesErr: errors.New("db down")},
			&fakeLocalStorage{},
			1024,
		)
		require.NoError(t, err)

		_, err = source.Categories(t.Context())

		require.ErrorIs(t, err, ErrUnavailable)
	})

	t.Run("object unavailable", func(t *testing.T) {
		repo := &fakeLocalRepository{get: &localPictureMetadata{
			ID: pictureID, Category: "A", Title: "B", MIMEType: "image/png",
			SizeBytes: 1, MinIOKey: LocalObjectPrefix + "/" + pictureID.String(),
		}}
		source, err := newLocalSource(repo, &fakeLocalStorage{getErr: errors.New("minio down")}, 1024)
		require.NoError(t, err)

		_, err = source.Image(t.Context(), pictureID.String())

		require.ErrorIs(t, err, ErrUnavailable)
	})

	t.Run("size mismatch", func(t *testing.T) {
		repo := &fakeLocalRepository{get: &localPictureMetadata{
			ID: pictureID, Category: "A", Title: "B", MIMEType: "image/png",
			SizeBytes: int64(len(testPNG()) + 1), MinIOKey: LocalObjectPrefix + "/" + pictureID.String(),
		}}
		source, err := newLocalSource(repo, &fakeLocalStorage{objects: map[string][]byte{
			repo.get.MinIOKey: testPNG(),
		}}, 1024*1024)
		require.NoError(t, err)

		_, err = source.Image(t.Context(), pictureID.String())

		require.ErrorIs(t, err, ErrInvalidResponse)
	})
}

func TestLocalSourcePicturesByCategory(t *testing.T) {
	transportID := uuid.New()
	cityID := uuid.New()
	repo := &fakeLocalRepository{
		categories: []string{"Транспорт", "Город"},
		pictures: []localPictureMetadata{
			{
				ID: transportID, Category: "Транспорт", Title: "Троллейбус",
				MIMEType: "image/png", SizeBytes: int64(len(testPNG())),
				MinIOKey: LocalObjectPrefix + "/" + transportID.String(),
			},
			{
				ID: cityID, Category: "Город", Title: "Улица",
				MIMEType: "image/jpeg", SizeBytes: int64(len(testPNG())),
				MinIOKey: LocalObjectPrefix + "/" + cityID.String(),
			},
		},
	}
	objectStorage := &fakeLocalStorage{objects: map[string][]byte{
		repo.pictures[0].MinIOKey: testPNG(),
		repo.pictures[1].MinIOKey: testPNG(),
	}}
	source, err := newLocalSource(repo, objectStorage, 1024*1024)
	require.NoError(t, err)

	t.Run("returns only pictures from requested category", func(t *testing.T) {
		pictures, err := source.PicturesByCategory(t.Context(), "Транспорт")
		require.NoError(t, err)
		require.Len(t, pictures, 1)
		assert.Equal(t, transportID.String(), pictures[0].ID)
		assert.Equal(t, "Троллейбус", pictures[0].Name)
		require.Len(t, pictures[0].Categories, 1)
		assert.Equal(t, "Транспорт", pictures[0].Categories[0].Name)
	})

	t.Run("empty result for unknown category", func(t *testing.T) {
		pictures, err := source.PicturesByCategory(t.Context(), "Несуществующая")
		require.NoError(t, err)
		assert.Empty(t, pictures)
	})

	t.Run("maps db error to ErrUnavailable", func(t *testing.T) {
		failingRepo := &fakeLocalRepository{searchErr: errors.New("db down")}
		src, err := newLocalSource(failingRepo, &fakeLocalStorage{}, 1024)
		require.NoError(t, err)

		_, err = src.PicturesByCategory(t.Context(), "Транспорт")
		require.ErrorIs(t, err, ErrUnavailable)
	})
}

type fakeLocalRepository struct {
	categories    []string
	categoriesErr error
	pictures      []localPictureMetadata
	searchErr     error
	get           *localPictureMetadata
	getErr        error
}

func (r *fakeLocalRepository) Categories(context.Context) ([]string, error) {
	return r.categories, r.categoriesErr
}

func (r *fakeLocalRepository) Search(context.Context, string) ([]localPictureMetadata, error) {
	return r.pictures, r.searchErr
}

func (r *fakeLocalRepository) Get(_ context.Context, id uuid.UUID) (*localPictureMetadata, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.get != nil {
		if r.get.ID != id {
			return nil, ErrPictureNotFound
		}
		return r.get, nil
	}
	for i := range r.pictures {
		if r.pictures[i].ID == id {
			return &r.pictures[i], nil
		}
	}
	return nil, ErrPictureNotFound
}

func (r *fakeLocalRepository) PicturesByCategory(_ context.Context, category string) ([]localPictureMetadata, error) {
	if r.searchErr != nil {
		return nil, r.searchErr
	}
	var result []localPictureMetadata
	for _, p := range r.pictures {
		if p.Category == category {
			result = append(result, p)
		}
	}
	return result, nil
}

type fakeLocalStorage struct {
	objects map[string][]byte
	getErr  error
}

func (s *fakeLocalStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	data, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}
