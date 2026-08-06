package ttsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/tts"
)

var (
	ErrBadRequest  = errors.New("ttsapi: bad request")
	ErrTooLarge    = errors.New("ttsapi: audio too large")
	ErrUnavailable = errors.New("ttsapi: service unavailable")
)

type Client struct {
	httpClient *http.Client
	apiURL     string
}

type ttsRequest struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
}

func NewClient(apiurl string, timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		apiURL:     apiurl,
	}
}

func (t *Client) Synthesize(ctx context.Context, text, voice string) ([]byte, error) {
	ttsReq := ttsRequest{Text: text, Voice: voice}

	data, err := json.Marshal(ttsReq)
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Synthesize: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.apiURL+"/tts", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Synthesize request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Synthesize: do request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Synthesize: read body: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest:
		return nil, ErrBadRequest
	case http.StatusRequestEntityTooLarge:
		return nil, ErrTooLarge
	case http.StatusBadGateway:
		return nil, ErrUnavailable
	default:
		return nil, fmt.Errorf("ttsapi.Synthesize: unexpected status %d", resp.StatusCode)
	}

	if len(audio) == 0 {
		return nil, fmt.Errorf("ttsapi.Synthesize: empty audio")
	}

	return audio, nil
}

type ttsVoice struct {
	LangCode string `json:"lang_code"`
	ID       string `json:"id"`
	Name     string `json:"name"`
}

func (t *Client) Voices(ctx context.Context) ([]tts.Voice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.apiURL+"/voices", nil)
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Voices: %w", err)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Voices: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ttsapi.Voices: unexpected status %d", resp.StatusCode)
	}

	var voicesFromAPI []ttsVoice
	err = json.NewDecoder(resp.Body).Decode(&voicesFromAPI)
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Voices: %w", err)
	}
	if len(voicesFromAPI) == 0 {
		return nil, fmt.Errorf("ttsapi.Voices: empty voice list")
	}

	voicesToService := make([]tts.Voice, len(voicesFromAPI))
	for i, v := range voicesFromAPI {
		voicesToService[i].ID = v.ID
		voicesToService[i].Name = v.Name
		voicesToService[i].LangCode = v.LangCode
	}

	return voicesToService, nil
}
