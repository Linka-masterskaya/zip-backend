//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/Linka-masterskaya/zip-backend/internal/httpapi"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/Linka-masterskaya/zip-backend/internal/tts"
	"github.com/Linka-masterskaya/zip-backend/internal/ttsapi"
	"github.com/Linka-masterskaya/zip-backend/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_TTSFlow(t *testing.T) {
	pool := e2eDatabase(t)
	userID := e2eUser(t, pool, "tts-user")
	token := e2eToken(t, userID, "defectologist")

	objectStorage, cleanupStorage := testutil.NewMinIO(t)
	t.Cleanup(cleanupStorage)

	fakeAudio := []byte("fake-mp3-data-for-e2e")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tts":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fakeAudio)
		case "/voices":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"id": "alena", "name": "Алёна", "lang_code": "ru-RU"},
				{"id": "filipp", "name": "Филипп", "lang_code": "ru-RU"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	ttsClient := ttsapi.NewClient(upstream.URL, 5*time.Second, 10)
	ttsRepo := tts.NewRepository(pool)
	pub := &fakeTTSPublisher{}
	ttsService := tts.NewService(ttsRepo, pub, ttsClient, tts.ServiceConfig{
		MaxTextLen: 5000,
		MimeType:   "audio/mpeg",
	})
	ttsHandler := tts.NewHandler(ttsService, 65536)
	ttsWorker := worker.NewTTS(ttsClient, objectStorage, ttsRepo, "audio/mpeg")

	mux := http.NewServeMux()
	auth := middleware.NewAuthMW([]byte(e2eJWTSecret))
	passthrough := func(next http.Handler) http.Handler { return next }
	httpapi.RegisterTTSRoutes(mux, auth, passthrough, httpapi.TTSHandlers{TTS: ttsHandler})
	httpapi.RegisterMediaRoutes(mux, auth, passthrough, httpapi.MediaHandlers{
		Media: media.NewHandler(media.NewService(media.NewRepository(pool), objectStorage)),
	})
	server := httptest.NewServer(middleware.Chain(
		mux,
		middleware.RecoveryMiddleware,
		middleware.RequestIDMiddleware,
	))
	t.Cleanup(server.Close)

	// 1. Создать задачу на озвучку
	resp := e2eRequest(t, server, token, http.MethodPost, "/api/v1/tts", map[string]any{
		"text": "привет", "voice": "alena",
	})
	createResult := e2eJSON[tts.TTSResponse](t, resp, http.StatusOK)
	require.NotEmpty(t, createResult.JobID)

	// 2. Статус — pending
	resp = e2eRequest(t, server, token, http.MethodGet, "/api/v1/tts/"+createResult.JobID, nil)
	pendingResult := e2eJSON[tts.TTSJobResponse](t, resp, http.StatusOK)
	assert.Equal(t, "pending", pendingResult.Status)
	assert.Nil(t, pendingResult.MediaID)

	// 3. Worker обрабатывает задачу
	require.NotNil(t, pub.lastJob)
	err := ttsWorker.Handle(context.Background(), *pub.lastJob, false)
	require.NoError(t, err)

	// 4. Статус — succeeded, есть media_id
	resp = e2eRequest(t, server, token, http.MethodGet, "/api/v1/tts/"+createResult.JobID, nil)
	doneResult := e2eJSON[tts.TTSJobResponse](t, resp, http.StatusOK)
	assert.Equal(t, "succeeded", doneResult.Status)
	require.NotNil(t, doneResult.MediaID)

	// 5. Медиафайл зарегистрирован
	resp = e2eRequest(t, server, token, http.MethodGet, "/api/v1/media/"+*doneResult.MediaID, nil)
	mediaInfo := e2eJSON[media.Response](t, resp, http.StatusOK)
	assert.Equal(t, "audio/mpeg", mediaInfo.MIMEType)
	assert.Equal(t, int64(len(fakeAudio)), mediaInfo.SizeBytes)

	// 6. Дедупликация — повторный запрос берёт из audio_bank
	resp = e2eRequest(t, server, token, http.MethodPost, "/api/v1/tts", map[string]any{
		"text": "привет", "voice": "alena",
	})
	dupResult := e2eJSON[tts.TTSResponse](t, resp, http.StatusOK)
	assert.NotEqual(t, createResult.JobID, dupResult.JobID, "bank hit creates new succeeded job")

	// Повторный job сразу succeeded
	resp = e2eRequest(t, server, token, http.MethodGet, "/api/v1/tts/"+dupResult.JobID, nil)
	dupStatus := e2eJSON[tts.TTSJobResponse](t, resp, http.StatusOK)
	assert.Equal(t, "succeeded", dupStatus.Status)
	require.NotNil(t, dupStatus.MediaID)

	// 7. Список голосов
	resp = e2eRequest(t, server, token, http.MethodGet, "/api/v1/tts/voices", nil)
	voicesResp := e2eJSON[tts.VoicesResponse](t, resp, http.StatusOK)
	assert.Len(t, voicesResp.Voices, 2)
	assert.Equal(t, "alena", voicesResp.Voices[0].ID)

	// 8. Пустой текст — 400
	resp = e2eRequest(t, server, token, http.MethodPost, "/api/v1/tts", map[string]any{
		"text": "", "voice": "alena",
	})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	e2eClose(t, resp)

	// 9. Невалидный UUID — 400
	resp = e2eRequest(t, server, token, http.MethodGet, "/api/v1/tts/not-a-uuid", nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	e2eClose(t, resp)

	// 10. Несуществующий job — 404
	resp = e2eRequest(t, server, token, http.MethodGet, "/api/v1/tts/00000000-0000-0000-0000-000000000099", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	e2eClose(t, resp)

	// 11. Без авторизации — 401
	resp = e2eRequest(t, server, "", http.MethodPost, "/api/v1/tts", map[string]any{
		"text": "тест", "voice": "alena",
	})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	e2eClose(t, resp)
}

type fakeTTSPublisher struct {
	lastJob *broker.TTSJob
}

func (f *fakeTTSPublisher) PublishTTSJob(_ context.Context, job broker.TTSJob) error {
	f.lastJob = &job
	return nil
}
