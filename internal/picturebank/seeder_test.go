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

func TestSeederStoresSystemContentInReservedNamespace(t *testing.T) {
	pictureID := uuid.New()
	repo := &fakeLocalAdminRepository{}
	objects := &fakeLocalAdminStorage{objects: make(map[string][]byte)}
	seeder := &Seeder{repo: repo, storage: objects, maxImageBytes: 1024 * 1024}

	createdID, err := seeder.Add(t.Context(), SeedInput{
		ID: pictureID, Category: " Животные ", Title: " Кот ", Data: testPNG(),
	})

	require.NoError(t, err)
	assert.Equal(t, pictureID, createdID)
	require.NotNil(t, repo.created)
	assert.Equal(t, "Животные", repo.created.Category)
	assert.Equal(t, "Кот", repo.created.Title)
	assert.Equal(t, "image/png", repo.created.MIMEType)
	assert.Equal(t, int64(len(testPNG())), repo.created.SizeBytes)
	assert.Equal(t, LocalObjectPrefix+"/"+pictureID.String(), repo.created.MinIOKey)
	assert.Equal(t, testPNG(), objects.objects[repo.created.MinIOKey])
	assert.NotContains(t, repo.created.MinIOKey, "media/")
}

func TestSeederDoesNotTouchObjectWhenMetadataInsertFails(t *testing.T) {
	objects := &fakeLocalAdminStorage{objects: make(map[string][]byte)}
	seeder := &Seeder{
		repo:    &fakeLocalAdminRepository{createErr: errors.New("db insert failed")},
		storage: objects, maxImageBytes: 1024 * 1024,
	}

	_, err := seeder.Add(t.Context(), SeedInput{
		Category: "Животные", Title: "Кот", Data: testPNG(),
	})

	require.ErrorContains(t, err, "db insert failed")
	assert.Empty(t, objects.objects)
	assert.Empty(t, objects.removed)
}

func TestSeederRemovesPossibleObjectAndMetadataWhenObjectWriteFails(t *testing.T) {
	pictureID := uuid.New()
	repo := &fakeLocalAdminRepository{}
	objects := &fakeLocalAdminStorage{
		objects:        make(map[string][]byte),
		putErr:         errors.New("minio write failed"),
		writeBeforeErr: true,
	}
	seeder := &Seeder{repo: repo, storage: objects, maxImageBytes: 1024 * 1024}

	_, err := seeder.Add(t.Context(), SeedInput{
		ID: pictureID, Category: "Животные", Title: "Кот", Data: testPNG(),
	})

	require.ErrorContains(t, err, "minio write failed")
	require.ErrorContains(t, err, pictureID.String())
	assert.Equal(t, []string{LocalObjectPrefix + "/" + pictureID.String()}, objects.removed)
	assert.True(t, repo.deleted)
	assert.Empty(t, objects.objects)
}

func TestSeederKeepsMetadataWhenAmbiguousObjectCleanupFails(t *testing.T) {
	pictureID := uuid.New()
	repo := &fakeLocalAdminRepository{}
	objects := &fakeLocalAdminStorage{
		objects:        make(map[string][]byte),
		putErr:         errors.New("minio write failed"),
		removeErr:      errors.New("minio cleanup failed"),
		writeBeforeErr: true,
	}
	seeder := &Seeder{repo: repo, storage: objects, maxImageBytes: 1024 * 1024}

	_, err := seeder.Add(t.Context(), SeedInput{
		ID: pictureID, Category: "Животные", Title: "Кот", Data: testPNG(),
	})

	require.ErrorContains(t, err, "minio write failed")
	require.ErrorContains(t, err, "minio cleanup failed")
	require.ErrorContains(t, err, pictureID.String())
	assert.False(t, repo.deleted, "metadata must remain so operator can retry delete")
	assert.Contains(t, objects.objects, LocalObjectPrefix+"/"+pictureID.String())
}

func TestSeederDeleteIsRestrictedToSystemNamespace(t *testing.T) {
	pictureID := uuid.New()
	repo := &fakeLocalAdminRepository{get: &localPictureMetadata{
		ID: pictureID, Category: "A", Title: "B", MIMEType: "image/png",
		SizeBytes: 1, MinIOKey: "media/org/user-object",
	}}
	objects := &fakeLocalAdminStorage{objects: map[string][]byte{"media/org/user-object": {1}}}
	seeder := &Seeder{repo: repo, storage: objects, maxImageBytes: 1024}

	err := seeder.Delete(t.Context(), pictureID)

	require.ErrorContains(t, err, "refusing to delete object outside")
	assert.Empty(t, objects.removed)
	assert.False(t, repo.deleted)
}

func TestSeederDeleteRemovesObjectAndMetadata(t *testing.T) {
	pictureID := uuid.New()
	key := LocalObjectPrefix + "/" + pictureID.String()
	repo := &fakeLocalAdminRepository{get: &localPictureMetadata{
		ID: pictureID, Category: "A", Title: "B", MIMEType: "image/png",
		SizeBytes: 1, MinIOKey: key,
	}}
	objects := &fakeLocalAdminStorage{objects: map[string][]byte{key: {1}}}
	seeder := &Seeder{repo: repo, storage: objects, maxImageBytes: 1024}

	err := seeder.Delete(t.Context(), pictureID)

	require.NoError(t, err)
	assert.Equal(t, []string{key}, objects.removed)
	assert.True(t, repo.deleted)
	assert.NotContains(t, objects.objects, key)
}

func TestSeederRejectsInvalidContentBeforeStorage(t *testing.T) {
	tests := []struct {
		name  string
		input SeedInput
	}{
		{name: "empty category", input: SeedInput{Title: "x", Data: testPNG()}},
		{name: "empty title", input: SeedInput{Category: "x", Data: testPNG()}},
		{name: "empty file", input: SeedInput{Category: "x", Title: "y"}},
		{name: "unsupported mime", input: SeedInput{Category: "x", Title: "y", Data: []byte("text")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			objects := &fakeLocalAdminStorage{objects: make(map[string][]byte)}
			seeder := &Seeder{repo: &fakeLocalAdminRepository{}, storage: objects, maxImageBytes: 1024 * 1024}

			_, err := seeder.Add(t.Context(), test.input)

			require.Error(t, err)
			assert.Empty(t, objects.objects)
		})
	}
}

type fakeLocalAdminRepository struct {
	get       *localPictureMetadata
	getErr    error
	created   *localPictureMetadata
	createErr error
	deleted   bool
	deleteErr error
}

func (r *fakeLocalAdminRepository) Get(context.Context, uuid.UUID) (*localPictureMetadata, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.get == nil {
		return nil, ErrPictureNotFound
	}
	return r.get, nil
}

func (r *fakeLocalAdminRepository) Create(_ context.Context, picture localPictureMetadata) error {
	copy := picture
	r.created = &copy
	return r.createErr
}

func (r *fakeLocalAdminRepository) Delete(context.Context, uuid.UUID) error {
	r.deleted = true
	return r.deleteErr
}

type fakeLocalAdminStorage struct {
	objects        map[string][]byte
	putErr         error
	getErr         error
	removeErr      error
	removed        []string
	writeBeforeErr bool
}

func (s *fakeLocalAdminStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	data, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *fakeLocalAdminStorage) PutObject(
	_ context.Context,
	key string,
	reader io.Reader,
	_ int64,
	_ string,
) error {
	if s.putErr != nil && !s.writeBeforeErr {
		return s.putErr
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if s.objects == nil {
		s.objects = make(map[string][]byte)
	}
	s.objects[key] = data
	return s.putErr
}

func (s *fakeLocalAdminStorage) RemoveObject(_ context.Context, key string) error {
	if s.removeErr != nil {
		return s.removeErr
	}
	delete(s.objects, key)
	s.removed = append(s.removed, key)
	return nil
}
