package picturebank

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type localRepoStub struct {
	files []*media.File
	file  *media.File
	err   error
}

func (s *localRepoStub) ListAccessible(
	context.Context,
	uuid.UUID,
	string,
	string,
	int,
	int,
) ([]*media.File, error) {
	return s.files, s.err
}

func (s *localRepoStub) GetOrganizationMedia(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*media.File, error) {
	return s.file, s.err
}

type localStorageStub struct {
	data []byte
	err  error
}

func (s *localStorageStub) GetObject(context.Context, string) (io.ReadCloser, error) {
	if s.err != nil {
		return nil, s.err
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func TestLocalClientUsesOrganizationMedia(t *testing.T) {
	userID, pictureID := uuid.New(), uuid.New()
	file := &media.File{
		ID: pictureID, Name: "Кот.png", MIMEType: "image/png",
		SizeBytes: 3, MinIOKey: "media/cat",
	}
	client, err := NewLocalClient(
		&localRepoStub{files: []*media.File{file}, file: file},
		&localStorageStub{data: []byte("png")},
		10,
	)
	require.NoError(t, err)
	ctx := authctx.SetUserIDToCtx(t.Context(), userID)

	categories, err := client.Categories(ctx)
	require.NoError(t, err)
	require.Len(t, categories, 1)
	assert.Equal(t, "local", categories[0].ID)

	pictures, err := client.Search(ctx, "кот")
	require.NoError(t, err)
	require.Len(t, pictures, 1)
	assert.Equal(t, pictureID.String(), pictures[0].ID)
	assert.Equal(t, "Кот.png", pictures[0].Name)

	image, err := client.Image(ctx, pictureID.String())
	require.NoError(t, err)
	assert.Equal(t, []byte("png"), image.Data)
	assert.Equal(t, "image/png", image.ContentType)
}

func TestLocalClientRejectsAudioAndOversizedImages(t *testing.T) {
	userID, pictureID := uuid.New(), uuid.New()
	ctx := authctx.SetUserIDToCtx(t.Context(), userID)
	for _, test := range []struct {
		name string
		file *media.File
		err  error
	}{
		{
			name: "audio is not a picture",
			file: &media.File{
				ID: pictureID, MIMEType: "audio/mpeg", SizeBytes: 3, MinIOKey: "media/audio",
			},
			err: ErrPictureNotFound,
		},
		{
			name: "image exceeds local response limit",
			file: &media.File{
				ID: pictureID, MIMEType: "image/png", SizeBytes: 11, MinIOKey: "media/large",
			},
			err: ErrResponseTooLarge,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewLocalClient(
				&localRepoStub{file: test.file}, &localStorageStub{data: []byte("data")}, 10,
			)
			require.NoError(t, err)
			_, err = client.Image(ctx, pictureID.String())
			require.ErrorIs(t, err, test.err)
		})
	}
}

func TestNewSourceFeatureFlagDoesNotRequireExternalLimiterInLocalMode(t *testing.T) {
	cfg := testPicturesConfig()
	cfg.URL = "https://pictures.example.test"
	local, err := NewSource(
		true,
		cfg,
		nil,
		&localRepoStub{},
		&localStorageStub{},
	)
	require.NoError(t, err)
	assert.IsType(t, &LocalClient{}, local)

	_, err = NewSource(false, cfg, nil, &localRepoStub{}, &localStorageStub{})
	require.ErrorContains(t, err, "distributed limiter is required")
}
