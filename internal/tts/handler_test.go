package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

type fakeService struct {
	createAudioFn func(context.Context, TTSDataRequest) (string, error)
	getJobFn      func(context.Context, uuid.UUID) (string, string, error)
	getVoicesFn   func(context.Context) ([]Voice, error)
}

func (f *fakeService) CreateAudio(ctx context.Context, req TTSDataRequest) (string, error) {
	if f.createAudioFn != nil {
		return f.createAudioFn(ctx, req)
	}
	return uuid.New().String(), nil
}

func (f *fakeService) GetJob(ctx context.Context, id uuid.UUID) (string, string, error) {
	if f.getJobFn != nil {
		return f.getJobFn(ctx, id)
	}
	return StatusPending, "", nil
}

func (f *fakeService) GetVoices(ctx context.Context) ([]Voice, error) {
	if f.getVoicesFn != nil {
		return f.getVoicesFn(ctx)
	}
	return nil, nil
}

func performRequest(
	t *testing.T,
	handler middleware.AppHandler,
	method, target string,
	body []byte,
	pathID string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, target, bytes.NewReader(body))
	if pathID != "" {
		req.SetPathValue("id", pathID)
	}
	rec := httptest.NewRecorder()
	middleware.ErrorMiddleware(handler).ServeHTTP(rec, req)
	return rec
}

func TestCreateOK(t *testing.T) {
	expectedJobID := uuid.New().String()
	svc := &fakeService{
		createAudioFn: func(_ context.Context, req TTSDataRequest) (string, error) {
			assert.Equal(t, "привет", req.Text)
			assert.Equal(t, "alena", req.Voice)
			return expectedJobID, nil
		},
	}
	h := NewHandler(svc, 1024)
	body := []byte(`{"text":"привет","voice":"alena"}`)

	rec := performRequest(t, h.Create, http.MethodPost, "/api/v1/tts", body, "")

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp TTSResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, expectedJobID, resp.JobID)
}

func TestCreateBodyTooLarge(t *testing.T) {
	h := NewHandler(&fakeService{}, 10)
	body := []byte(`{"text":"` + strings.Repeat("a", 100) + `","voice":"alena"}`)

	rec := performRequest(t, h.Create, http.MethodPost, "/api/v1/tts", body, "")

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestCreateInvalidJSON(t *testing.T) {
	h := NewHandler(&fakeService{}, 1024)

	rec := performRequest(t, h.Create, http.MethodPost, "/api/v1/tts", []byte(`{broken`), "")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetPending(t *testing.T) {
	svc := &fakeService{
		getJobFn: func(_ context.Context, _ uuid.UUID) (string, string, error) {
			return StatusPending, "", nil
		},
	}
	h := NewHandler(svc, 1024)
	jobID := uuid.New()

	rec := performRequest(t, h.Get, http.MethodGet, "/api/v1/tts/"+jobID.String(), nil, jobID.String())

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp TTSJobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, StatusPending, resp.Status)
	assert.Nil(t, resp.MediaID)
}

func TestGetSucceeded(t *testing.T) {
	mediaID := uuid.New().String()
	svc := &fakeService{
		getJobFn: func(_ context.Context, _ uuid.UUID) (string, string, error) {
			return StatusSucceeded, mediaID, nil
		},
	}
	h := NewHandler(svc, 1024)
	jobID := uuid.New()

	rec := performRequest(t, h.Get, http.MethodGet, "/api/v1/tts/"+jobID.String(), nil, jobID.String())

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp TTSJobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, StatusSucceeded, resp.Status)
	require.NotNil(t, resp.MediaID)
	assert.Equal(t, mediaID, *resp.MediaID)
}

func TestGetInvalidUUID(t *testing.T) {
	h := NewHandler(&fakeService{}, 1024)

	rec := performRequest(t, h.Get, http.MethodGet, "/api/v1/tts/not-a-uuid", nil, "not-a-uuid")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVoicesOK(t *testing.T) {
	svc := &fakeService{
		getVoicesFn: func(_ context.Context) ([]Voice, error) {
			return []Voice{
				{ID: "alena", Name: "Алёна", LangCode: "ru-RU"},
				{ID: "john", Name: "Джон", LangCode: "en-US"},
			}, nil
		},
	}
	h := NewHandler(svc, 1024)

	rec := performRequest(t, h.Voices, http.MethodGet, "/api/v1/tts/voices", nil, "")

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp VoicesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Voices, 2)
	assert.Equal(t, "alena", resp.Voices[0].ID)
	assert.Equal(t, "Алёна (ru-RU)", resp.Voices[0].Name)
	assert.Equal(t, "john", resp.Voices[1].ID)
	assert.Equal(t, "Джон (en-US)", resp.Voices[1].Name)
}
