package ttsapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/tts"
	"github.com/Linka-masterskaya/zip-backend/internal/ttsapi"
)

func testClient(t *testing.T, handler http.HandlerFunc) *ttsapi.Client {
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return ttsapi.NewClient(srv.URL, 5*time.Second, 10)
}

func TestSynthesizeOK(t *testing.T) {
	audio := []byte("fake-mp3-data")
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/tts", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req struct {
			Text  string `json:"text"`
			Voice string `json:"voice"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "привет", req.Text)
		assert.Equal(t, "alena", req.Voice)

		w.WriteHeader(http.StatusOK)
		w.Write(audio)
	})

	got, err := client.Synthesize(context.Background(), "привет", "alena")
	require.NoError(t, err)
	assert.Equal(t, audio, got)
}

func TestSynthesizeBadRequest(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	_, err := client.Synthesize(context.Background(), "", "alena")
	require.ErrorIs(t, err, ttsapi.ErrBadRequest)
}

func TestSynthesizeTooLarge(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	})

	_, err := client.Synthesize(context.Background(), "long text", "alena")
	require.ErrorIs(t, err, ttsapi.ErrTooLarge)
}

func TestSynthesizeUnavailable(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	_, err := client.Synthesize(context.Background(), "привет", "alena")
	require.ErrorIs(t, err, ttsapi.ErrUnavailable)
}

func TestSynthesizeEmptyAudio(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := client.Synthesize(context.Background(), "привет", "alena")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty audio")
}

func TestVoicesOK(t *testing.T) {
	apiResponse := []map[string]any{
		{
			"lang_code": "ru-RU",
			"lang":      "Русский",
			"id":        "alena",
			"name":      "Алёна",
			"gender":    "F",
			"role":      []string{"neutral", "good"},
			"engine":    "yandex",
		},
		{
			"lang_code": "en-US",
			"lang":      "Английский",
			"id":        "john",
			"name":      "Джон",
			"gender":    "M",
			"role":      nil,
			"engine":    "yandex",
		},
	}

	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/voices", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(apiResponse)
	})

	voices, err := client.Voices(context.Background())
	require.NoError(t, err)
	require.Len(t, voices, 2)

	assert.Equal(t, tts.Voice{ID: "alena", Name: "Алёна", LangCode: "ru-RU"}, voices[0])
	assert.Equal(t, tts.Voice{ID: "john", Name: "Джон", LangCode: "en-US"}, voices[1])
}

func TestVoicesEmpty(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	})

	_, err := client.Voices(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty voice list")
}

func TestVoicesServerError(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := client.Voices(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 500")
}
func TestSynthesizeUnexpectedStatus(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := client.Synthesize(context.Background(), "привет", "alena")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 500")
}

func TestVoicesInvalidJSON(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json`))
	})

	_, err := client.Voices(context.Background())
	require.Error(t, err)
}
