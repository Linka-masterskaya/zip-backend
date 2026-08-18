package pack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const linkaLooksFixtureDir = "../../docs/compatibility/linka-looks/testdata"

// TestLinkaLooksCompatibilityFixture proves that the checked-in N5 fixture is
// produced by the same HTTP handler/service path that backs
// GET /api/v1/packs/{id}/export. It intentionally does not assert that Linka
// Looks can consume Linka Config 2.0; that observation is executed separately
// against the pinned Linka Looks v3.2.10 parser.
func TestLinkaLooksCompatibilityFixture(t *testing.T) {
	imageID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	audioID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	packID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	userID := uuid.MustParse("44444444-4444-4444-8444-444444444444")

	sourceConfig := mustReadCompatFile(t, "source-config.json")
	image := mustReadCompatFile(t, "pixel.png")
	audio := mustReadCompatFile(t, "tone.wav")

	repo := &exportContentRepository{
		pack: &Pack{ID: packID, Title: "N5 Linka Looks compatibility", Config: json.RawMessage(sourceConfig)},
		files: []*media.File{
			{ID: imageID, MIMEType: "image/png", SizeBytes: int64(len(image)), MinIOKey: "n5/pixel.png"},
			{ID: audioID, MIMEType: "audio/wav", SizeBytes: int64(len(audio)), MinIOKey: "n5/tone.wav"},
		},
	}
	service := NewContentService(repo, fakeArchiveStorage{objects: map[string][]byte{
		"n5/pixel.png": image,
		"n5/tone.wav":  audio,
	}}, nil, nil)
	handler := NewContentHandler(service)

	req := httptest.NewRequestWithContext(
		packContext(userID),
		http.MethodGet,
		"/api/v1/packs/"+packID.String()+"/export",
		nil,
	)
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/packs/{id}/export", middleware.ErrorMiddleware(handler.Export))
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "application/vnd.linka+zip", rec.Header().Get("Content-Type"))
	mediaType, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
	require.NoError(t, err)
	assert.Equal(t, "attachment", mediaType)
	assert.Equal(t, "N5 Linka Looks compatibility.linka", params["filename"])

	exported := append([]byte(nil), rec.Body.Bytes()...)
	if os.Getenv("UPDATE_LINKA_LOOKS_FIXTURE") == "1" {
		require.NoError(t, os.WriteFile(filepath.Join(linkaLooksFixtureDir, "backend-v2-export.linka"), exported, 0o644))
	}

	actualEntries := readCompatArchive(t, exported)
	goldenEntries := readCompatArchive(t, mustReadCompatFile(t, "backend-v2-export.linka"))

	assert.Equal(t, sortedCompatKeys(goldenEntries), sortedCompatKeys(actualEntries))
	assert.Equal(t, image, actualEntries["media/"+imageID.String()+".png"])
	assert.Equal(t, audio, actualEntries["media/"+audioID.String()+".wav"])
	require.JSONEq(t, string(goldenEntries["config.json"]), string(actualEntries["config.json"]))

	var cfg linka.Config
	require.NoError(t, json.Unmarshal(actualEntries["config.json"], &cfg))
	require.Len(t, cfg.Blocks, 2)
	assert.Equal(t, []string{"quiz-block", "matching-block"}, []string{cfg.Blocks[0].ID, cfg.Blocks[1].ID})
	assert.Equal(t, []string{linka.BlockTypeSingleChoice, linka.BlockTypeMatching}, []string{cfg.Blocks[0].Type, cfg.Blocks[1].Type})

	require.Len(t, cfg.Blocks[0].Elements, 3)
	assert.Equal(t, []string{"text-yozhik", "image-one", "audio-one"}, []string{
		cfg.Blocks[0].Elements[0].ID,
		cfg.Blocks[0].Elements[1].ID,
		cfg.Blocks[0].Elements[2].ID,
	})
	assert.Equal(t, "Ёжик — правильный ответ", cfg.Blocks[0].Elements[0].Value)
	assert.Equal(t, "media/"+imageID.String()+".png", cfg.Blocks[0].Elements[1].MediaURL)
	assert.Equal(t, "media/"+audioID.String()+".wav", cfg.Blocks[0].Elements[2].MediaURL)

	require.Len(t, cfg.Blocks[1].Elements, 4)
	assert.Equal(t, []string{"cat-ru", "cat-en", "dog-ru", "dog-en"}, []string{
		cfg.Blocks[1].Elements[0].ID,
		cfg.Blocks[1].Elements[1].ID,
		cfg.Blocks[1].Elements[2].ID,
		cfg.Blocks[1].Elements[3].ID,
	})
	assert.Equal(t, []string{"Кошка", "cat", "Собака", "dog"}, []string{
		cfg.Blocks[1].Elements[0].Value,
		cfg.Blocks[1].Elements[1].Value,
		cfg.Blocks[1].Elements[2].Value,
		cfg.Blocks[1].Elements[3].Value,
	})
}

func mustReadCompatFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(linkaLooksFixtureDir, name))
	require.NoError(t, err)
	return data
}

func readCompatArchive(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	entries := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		entry, err := file.Open()
		require.NoError(t, err)
		content, err := io.ReadAll(entry)
		closeErr := entry.Close()
		require.NoError(t, err)
		require.NoError(t, closeErr)
		entries[file.Name] = content
	}
	return entries
}

func sortedCompatKeys(entries map[string][]byte) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
