package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/media"
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
	data, err := buildArchive(
		context.Background(), config, []*media.File{file},
		fakeArchiveStorage{objects: map[string][]byte{"object": {1, 2, 3}}},
	)
	require.NoError(t, err)
	parsed, err := parseArchive(data)
	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3}, parsed.Files["media/"+mediaID.String()+".png"])
	var exported linka.Config
	require.NoError(t, json.Unmarshal(parsed.Config, &exported))
	assert.Equal(t, "media/"+mediaID.String()+".png", exported.Blocks[0].Elements[0].MediaURL)
	assert.Equal(t, mediaID, *exported.Blocks[0].Elements[0].MediaID)
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
}

func (s fakeArchiveStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.objects[key])), nil
}
