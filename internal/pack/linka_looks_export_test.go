package pack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exportFixtureRequest прогоняет тот же HTTP-маршрут, что и боевой
// GET /api/v1/packs/{id}/export, на фикстуре спайка N5.
func exportFixtureRequest(t *testing.T, query string, config []byte) *httptest.ResponseRecorder {
	t.Helper()
	imageID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	audioID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	packID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	userID := uuid.MustParse("44444444-4444-4444-8444-444444444444")

	image := mustReadCompatFile(t, "pixel.png")
	audio := mustReadCompatFile(t, "tone.wav")

	repo := &exportContentRepository{
		pack: &Pack{ID: packID, Title: "N5 Linka Looks export", Config: json.RawMessage(config)},
		files: []*media.File{
			{ID: imageID, MIMEType: "image/png", SizeBytes: int64(len(image)), MinIOKey: "n5/pixel.png"},
			{ID: audioID, MIMEType: "audio/wav", SizeBytes: int64(len(audio)), MinIOKey: "n5/tone.wav"},
		},
	}
	service := NewContentService(repo, fakeArchiveStorage{objects: map[string][]byte{
		"n5/pixel.png": image,
		"n5/tone.wav":  audio,
	}}, nil, nil)

	req := httptest.NewRequestWithContext(
		packContext(userID),
		http.MethodGet,
		"/api/v1/packs/"+packID.String()+"/export"+query,
		nil,
	)
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/packs/{id}/export", middleware.ErrorMiddleware(NewContentHandler(service).Export))
	mux.ServeHTTP(rec, req)
	return rec
}

// TestExportLooks3ProducesLooksSet — регрессия на находку спайка N5:
// до этого экспорт всегда отдавал Linka Config 2.0, который Linka
// Looks 3.2.10 молча превращал в пустую страницу.
func TestExportLooks3ProducesLooksSet(t *testing.T) {
	rec := exportFixtureRequest(t, "?format=looks-3", mustReadCompatFile(t, "source-config.json"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Golden нужен, чтобы прогнать архив через официальный парсер
	// Linka Looks: docs/compatibility/linka-looks/README.md, шаг 2.
	if os.Getenv("UPDATE_LINKA_LOOKS_FIXTURE") == "1" {
		require.NoError(t, os.WriteFile(
			filepath.Join(linkaLooksFixtureDir, "backend-looks3-export.linka"),
			rec.Body.Bytes(), 0o644,
		))
	}

	entries := readCompatArchive(t, rec.Body.Bytes())
	require.Contains(t, entries, "config.json")

	var looks linka.LooksConfig
	require.NoError(t, json.Unmarshal(entries["config.json"], &looks))
	assert.Equal(t, linka.LooksSetVersion, looks.Version)
	require.Len(t, looks.Pages, 2, "каждый блок должен стать страницей")

	quiz := looks.Pages[0]
	assert.Equal(t, linka.LooksModeQuiz, quiz.Mode)
	assert.Equal(t, "Ёжик — правильный ответ", quiz.Cards[0].Title)
	require.NotNil(t, quiz.Cards[0].Answer)
	assert.True(t, *quiz.Cards[0].Answer)
	// Пути медиа должны указывать на реальные файлы внутри архива,
	// иначе Looks покажет карточку без картинки и звука.
	assert.Contains(t, entries, quiz.Cards[1].ImagePath)
	assert.Contains(t, entries, quiz.Cards[2].AudioPath)

	match := looks.Pages[1]
	assert.Equal(t, linka.LooksModeMatch, match.Mode)
	require.Len(t, match.Cards, 4)
	assert.Equal(t, match.Cards[0].MatchID, match.Cards[2].MatchID)
}

// TestExportDefaultFormatStaysLinka2 фиксирует обратную
// совместимость: без параметра формат не меняется.
func TestExportDefaultFormatStaysLinka2(t *testing.T) {
	for _, query := range []string{"", "?format=linka-2"} {
		rec := exportFixtureRequest(t, query, mustReadCompatFile(t, "source-config.json"))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		entries := readCompatArchive(t, rec.Body.Bytes())
		var cfg linka.Config
		require.NoError(t, json.Unmarshal(entries["config.json"], &cfg))
		assert.Equal(t, "2.0", cfg.Metadata.Version)
		require.Len(t, cfg.Blocks, 2)
	}
}

func TestExportRejectsUnknownFormat(t *testing.T) {
	rec := exportFixtureRequest(t, "?format=looks-9", mustReadCompatFile(t, "source-config.json"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestExportLooks3RejectsUnrepresentableBlock — набор валиден, но в
// Looks не выражается. Лучше явная ошибка, чем архив с потерянным
// заданием.
func TestExportLooks3RejectsUnrepresentableBlock(t *testing.T) {
	config := []byte(`{
		"metadata": {"version": "2.0", "title": "multi"},
		"settings": {"columns": 2, "rows": 2},
		"blocks": [{
			"id": "multi-block",
			"type": "multi_choice",
			"elements": [
				{"id": "a", "kind": "text", "value": "a"},
				{"id": "b", "kind": "text", "value": "b"}
			],
			"answers": [
				{"element_id": "a", "is_correct": true},
				{"element_id": "b", "is_correct": true}
			]
		}]
	}`)
	rec := exportFixtureRequest(t, "?format=looks-3", config)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "multi-block")
}
