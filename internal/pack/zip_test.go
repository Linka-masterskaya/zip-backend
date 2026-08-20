package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArchiveRejectsUnsafePaths(t *testing.T) {
	data := testZIP(t, map[string][]byte{
		"config.json":  []byte(`{}`),
		"../photo.png": []byte("not safe"),
	})
	_, err := parseArchive(data)
	require.ErrorIs(t, err, ErrInvalidArchive)
}

func TestParseArchiveRequiresConfig(t *testing.T) {
	data := testZIP(t, map[string][]byte{"media/photo.png": []byte("png")})
	_, err := parseArchive(data)
	require.ErrorIs(t, err, ErrInvalidArchive)
}

func TestBuildAndParseArchiveRoundTrip(t *testing.T) {
	mediaID := uuid.New()
	cfg := linka.Config{
		Metadata: linka.Metadata{Version: "2.0"},
		Settings: linka.Settings{Columns: 1, Rows: 1},
		Blocks: []linka.Block{{
			ID: "b", Type: linka.BlockTypeGrid,
			Elements: []linka.Element{{
				ID: "e", Kind: linka.ElementKindImage, MediaID: &mediaID,
			}},
		}},
	}
	config, err := json.Marshal(cfg)
	require.NoError(t, err)
	file := &media.File{
		ID: mediaID, MIMEType: "image/png", SizeBytes: 3, MinIOKey: "object",
	}
	archive, err := buildArchive(
		context.Background(), config, []*media.File{file},
		fakeArchiveStorage{objects: map[string][]byte{"object": {1, 2, 3}}},
		ExportFormatLinka2,
	)
	require.NoError(t, err)
	path := archive.path
	data := readArchive(t, archive)
	assert.EqualValues(t, len(data), archive.size)
	require.NoError(t, archive.Close())
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)

	parsed, err := parseArchive(data)
	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3}, parsed.Files["media/"+mediaID.String()+".png"])
	var exported linka.Config
	require.NoError(t, json.Unmarshal(parsed.Config, &exported))
	assert.Equal(t, "media/"+mediaID.String()+".png", exported.Blocks[0].Elements[0].MediaURL)
	assert.Equal(t, mediaID, *exported.Blocks[0].Elements[0].MediaID)
}

func TestBuildArchiveRejectsMissingMediaMetadata(t *testing.T) {
	mediaID := uuid.New()
	config := archiveConfigWithMediaID(mediaID)
	_, err := buildArchive(context.Background(), config, nil, fakeArchiveStorage{}, ExportFormatLinka2)
	require.ErrorIs(t, err, ErrMissingMediaReference)
}

func TestBuildArchiveRejectsMissingStorageObject(t *testing.T) {
	temporaryDir := t.TempDir()
	t.Setenv("TMPDIR", temporaryDir)
	mediaID := uuid.New()
	file := &media.File{
		ID: mediaID, MIMEType: "image/png", SizeBytes: 3, MinIOKey: "missing",
	}
	_, err := buildArchive(
		context.Background(), archiveConfigWithMediaID(mediaID), []*media.File{file},
		fakeArchiveStorage{objects: map[string][]byte{}},
		ExportFormatLinka2,
	)
	require.ErrorIs(t, err, ErrMissingMediaReference)
	entries, readErr := os.ReadDir(temporaryDir)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func TestBuildArchiveEnforcesFinalZIPLimit(t *testing.T) {
	temporaryDir := t.TempDir()
	t.Setenv("TMPDIR", temporaryDir)
	config := json.RawMessage(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":1},
		"blocks":[]
	}`)

	_, err := buildArchiveWithLimit(
		context.Background(), config, nil, nil, nil, ExportFormatLinka2, 64,
	)

	require.ErrorIs(t, err, ErrArchiveTooLarge)
	entries, readErr := os.ReadDir(temporaryDir)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func TestArchiveLimitWriterRejectsOverflow(t *testing.T) {
	var target bytes.Buffer
	writer := &archiveLimitWriter{writer: &target, remaining: 3}
	written, err := writer.Write([]byte{1, 2, 3, 4})
	assert.Equal(t, 3, written)
	require.ErrorIs(t, err, ErrArchiveTooLarge)
	assert.Equal(t, []byte{1, 2, 3}, target.Bytes())
}

func TestValidateAndMediaIDsRequiresStoredReference(t *testing.T) {
	config := json.RawMessage(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":1},
		"blocks":[{"id":"b","type":"grid","elements":[{"id":"e","kind":"image"}]}]
	}`)
	_, err := validateAndMediaIDs(context.Background(), config, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "media_id")
}

func TestValidateAndMediaIDsAllowsPicturesBankReferenceWithoutMedia(t *testing.T) {
	pictureID := uuid.New()
	config := json.RawMessage(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":1},
		"blocks":[{"id":"b","type":"grid","elements":[{
			"id":"e","kind":"image","source_picture_id":"` + pictureID.String() + `"
		}]}]
	}`)
	ids, err := validateAndMediaIDs(context.Background(), config, false)
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestBuildArchiveResolvesPicturesBankReferenceWithoutLocalStorage(t *testing.T) {
	pictureID := uuid.New()
	config := json.RawMessage(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":1},
		"blocks":[{"id":"b","type":"grid","elements":[{
			"id":"e","kind":"image","source_picture_id":"` + pictureID.String() + `"
		}]}]
	}`)
	archive, err := buildArchive(
		context.Background(), config, nil, fakeArchiveStorage{},
		ExportFormatLinka2,
		func(_ context.Context, id uuid.UUID) ([]byte, string, error) {
			assert.Equal(t, pictureID, id)
			return []byte{1, 2, 3}, "image/png", nil
		},
	)
	require.NoError(t, err)
	data := readArchive(t, archive)
	require.NoError(t, archive.Close())
	parsed, err := parseArchive(data)
	require.NoError(t, err)
	path := "media/picture-" + pictureID.String() + ".png"
	assert.Equal(t, []byte{1, 2, 3}, parsed.Files[path])
	assert.Contains(t, string(parsed.Config), path)
	assert.Contains(t, string(parsed.Config), pictureID.String())
}

func TestBuildArchiveRejectsMissingPicturesBankReference(t *testing.T) {
	pictureID := uuid.New()
	config := json.RawMessage(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":1},
		"blocks":[{"id":"b","type":"grid","elements":[{
			"id":"e","kind":"image","source_picture_id":"` + pictureID.String() + `"
		}]}]
	}`)
	_, err := buildArchive(context.Background(), config, nil, fakeArchiveStorage{}, ExportFormatLinka2)
	require.ErrorIs(t, err, ErrMissingMediaReference)
}

func archiveConfigWithMediaID(mediaID uuid.UUID) json.RawMessage {
	return json.RawMessage(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":1},
		"blocks":[{"id":"b","type":"grid","elements":[{
			"id":"e","kind":"image","media_id":"` + mediaID.String() + `"
		}]}]
	}`)
}

func readArchive(t *testing.T, archive io.Reader) []byte {
	t.Helper()
	data, err := io.ReadAll(archive)
	require.NoError(t, err)
	return data
}

func testZIP(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range entries {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

type fakeArchiveStorage struct {
	objects map[string][]byte
	err     error
}

func (s fakeArchiveStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	if s.err != nil {
		return nil, s.err
	}
	data, exists := s.objects[key]
	if !exists {
		return nil, storage.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func TestContentErrorMapsMissingMediaToConflict(t *testing.T) {
	err := contentError(fmt.Errorf("export failed: %w", ErrMissingMediaReference))
	var appErr *apperr.AppError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, http.StatusConflict, appErr.HTTPStatus)
}
