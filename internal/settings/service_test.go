package settings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/tts"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRepository struct {
	getFn            func(context.Context, uuid.UUID) (json.RawMessage, error)
	putFn            func(context.Context, uuid.UUID, json.RawMessage) error
	listTemplatesFn  func(context.Context, uuid.UUID) ([]Template, error)
	createTemplateFn func(context.Context, uuid.UUID, string, json.RawMessage) (*Template, error)
	deleteTemplateFn func(context.Context, uuid.UUID, uuid.UUID) error
}

func (f *fakeRepository) Get(ctx context.Context, id uuid.UUID) (json.RawMessage, error) {
	if f.getFn != nil {
		return f.getFn(ctx, id)
	}
	return json.RawMessage(`{}`), nil
}

func (f *fakeRepository) Put(ctx context.Context, id uuid.UUID, body json.RawMessage) error {
	if f.putFn != nil {
		return f.putFn(ctx, id, body)
	}
	return nil
}

func (f *fakeRepository) ListTemplates(ctx context.Context, id uuid.UUID) ([]Template, error) {
	if f.listTemplatesFn != nil {
		return f.listTemplatesFn(ctx, id)
	}
	return []Template{}, nil
}

func (f *fakeRepository) CreateTemplate(ctx context.Context, id uuid.UUID, name string, body json.RawMessage) (*Template, error) {
	if f.createTemplateFn != nil {
		return f.createTemplateFn(ctx, id, name, body)
	}
	return &Template{ID: uuid.New(), Name: name, Body: body}, nil
}

func (f *fakeRepository) DeleteTemplate(ctx context.Context, userID, templateID uuid.UUID) error {
	if f.deleteTemplateFn != nil {
		return f.deleteTemplateFn(ctx, userID, templateID)
	}
	return nil
}

type fakeVoiceCatalog struct {
	voices []tts.Voice
	err    error
	calls  int
}

func (f *fakeVoiceCatalog) GetVoices(context.Context) ([]tts.Voice, error) {
	f.calls++
	return f.voices, f.err
}

func userContext() (context.Context, uuid.UUID) {
	id := uuid.New()
	return authctx.SetUserIDToCtx(context.Background(), id), id
}

func TestServicePutValidSettingsAndVoice(t *testing.T) {
	ctx, userID := userContext()
	voices := &fakeVoiceCatalog{voices: []tts.Voice{{ID: "alena"}}}
	var persisted json.RawMessage
	repo := &fakeRepository{putFn: func(_ context.Context, gotID uuid.UUID, body json.RawMessage) error {
		assert.Equal(t, userID, gotID)
		persisted = append(json.RawMessage(nil), body...)
		return nil
	}}
	svc := NewService(repo, voices)
	body := json.RawMessage(`{"voice":"alena","colors":{"background":"#fff"},"border_width":2}`)

	got, err := svc.Put(ctx, body)
	require.NoError(t, err)
	assert.JSONEq(t, string(body), string(got))
	assert.JSONEq(t, string(body), string(persisted))
	assert.Equal(t, 1, voices.calls)
}

func TestServicePutRejectsNonObjectUnknownKeyAndInvalidVoice(t *testing.T) {
	ctx, _ := userContext()
	voices := &fakeVoiceCatalog{voices: []tts.Voice{{ID: "alena"}}}
	svc := NewService(&fakeRepository{}, voices)

	tests := []struct {
		name string
		body string
		msg  string
	}{
		{name: "array", body: `[]`, msg: "settings must be a JSON object"},
		{name: "unknown key", body: `{"unexpected":true}`, msg: "unsupported settings key: unexpected"},
		{name: "voice type", body: `{"voice":17}`, msg: "voice must be a non-empty string"},
		{name: "unknown voice", body: `{"voice":"john"}`, msg: "unknown voice"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Put(ctx, json.RawMessage(tt.body))
			require.Error(t, err)
			var appErr *apperr.AppError
			require.ErrorAs(t, err, &appErr)
			assert.Equal(t, tt.msg, appErr.Message)
		})
	}
}

func TestServicePutSkipsVoiceValidationWhenCatalogUnavailable(t *testing.T) {
	ctx, _ := userContext()
	catalog := &fakeVoiceCatalog{err: errors.New("tts down")}
	svc := NewService(&fakeRepository{}, catalog)

	_, err := svc.Put(ctx, json.RawMessage(`{"voice":"custom"}`))
	require.NoError(t, err)
	assert.Equal(t, 1, catalog.calls)
}

func TestServicePutRejectsVoiceWhenAvailableCatalogIsEmpty(t *testing.T) {
	ctx, _ := userContext()
	catalog := &fakeVoiceCatalog{voices: []tts.Voice{}}
	svc := NewService(&fakeRepository{}, catalog)

	_, err := svc.Put(ctx, json.RawMessage(`{"voice":"custom"}`))
	require.Error(t, err)
	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "unknown voice", appErr.Message)
	assert.Equal(t, 1, catalog.calls)
}

func TestServicePutDoesNotFetchVoicesWithoutVoiceSetting(t *testing.T) {
	ctx, _ := userContext()
	catalog := &fakeVoiceCatalog{voices: []tts.Voice{{ID: "alena"}}}
	svc := NewService(&fakeRepository{}, catalog)

	_, err := svc.Put(ctx, json.RawMessage(`{"colors":{"accent":"blue"}}`))
	require.NoError(t, err)
	assert.Zero(t, catalog.calls)
}

func TestServicePutRejectsDocumentOverLimit(t *testing.T) {
	ctx, _ := userContext()
	svc := NewService(&fakeRepository{}, nil)
	body := json.RawMessage(`{"colors":"` + strings.Repeat("x", MaxDocumentSize) + `"}`)

	_, err := svc.Put(ctx, body)
	require.Error(t, err)
	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, apperr.ErrPayloadTooLarge.HTTPStatus, appErr.HTTPStatus)
}

func TestServiceTemplateIsUserScopedAndTrimsName(t *testing.T) {
	ctx, userID := userContext()
	templateID := uuid.New()
	repo := &fakeRepository{
		createTemplateFn: func(_ context.Context, gotID uuid.UUID, name string, body json.RawMessage) (*Template, error) {
			assert.Equal(t, userID, gotID)
			assert.Equal(t, "High contrast", name)
			return &Template{ID: templateID, Name: name, Body: body}, nil
		},
		deleteTemplateFn: func(_ context.Context, gotUserID, gotTemplateID uuid.UUID) error {
			assert.Equal(t, userID, gotUserID)
			assert.Equal(t, templateID, gotTemplateID)
			return nil
		},
	}
	svc := NewService(repo, nil)

	created, err := svc.CreateTemplate(ctx, "  High contrast  ", json.RawMessage(`{"colors":{"background":"black"}}`))
	require.NoError(t, err)
	assert.Equal(t, "High contrast", created.Name)
	require.NoError(t, svc.DeleteTemplate(ctx, templateID))
}

func TestServiceRequiresAuthenticatedUser(t *testing.T) {
	svc := NewService(&fakeRepository{}, nil)
	_, err := svc.Get(context.Background())
	require.ErrorIs(t, err, apperr.ErrUnauthorized)
}

func TestServiceV1ContractAcceptsAllDeclaredKeys(t *testing.T) {
	ctx, _ := userContext()
	catalog := &fakeVoiceCatalog{voices: []tts.Voice{{ID: "alena"}}}
	svc := NewService(&fakeRepository{}, catalog)

	body := json.RawMessage(`{
		"eye_control":{"enabled":true},
		"card_activation":true,
		"interactivity":["future","opaque","shape"],
		"voice":"alena",
		"button_direction":"forward",
		"colors":{"background":"#fff"},
		"border_width":2
	}`)

	_, err := svc.Put(ctx, body)
	require.NoError(t, err)
	assert.Equal(t, 1, catalog.calls)
}

func TestServiceDocumentSizeBoundary(t *testing.T) {
	ctx, _ := userContext()
	svc := NewService(&fakeRepository{}, nil)

	const prefix = `{"colors":"`
	const suffix = `"}`
	atLimit := json.RawMessage(prefix + strings.Repeat("x", MaxDocumentSize-len(prefix)-len(suffix)) + suffix)
	require.Len(t, atLimit, MaxDocumentSize)

	_, err := svc.Put(ctx, atLimit)
	require.NoError(t, err)

	overLimit := append(append(json.RawMessage(nil), atLimit[:len(atLimit)-len(suffix)]...), 'x')
	overLimit = append(overLimit, suffix...)
	require.Len(t, overLimit, MaxDocumentSize+1)

	_, err = svc.Put(ctx, overLimit)
	require.Error(t, err)
	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusRequestEntityTooLarge, appErr.HTTPStatus)
}

func TestServiceTemplateUsesSameVoiceValidation(t *testing.T) {
	ctx, _ := userContext()
	catalog := &fakeVoiceCatalog{voices: []tts.Voice{{ID: "alena"}}}
	svc := NewService(&fakeRepository{}, catalog)

	_, err := svc.CreateTemplate(ctx, "Known voice", json.RawMessage(`{"voice":"alena"}`))
	require.NoError(t, err)

	_, err = svc.CreateTemplate(ctx, "Unknown voice", json.RawMessage(`{"voice":"john"}`))
	require.Error(t, err)
	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "unknown voice", appErr.Message)
}

func TestServiceV1RejectsDeferredTopLevelKeys(t *testing.T) {
	ctx, _ := userContext()
	svc := NewService(&fakeRepository{}, nil)

	for _, key := range []string{"calibration_offset", "media_bank", "audio_bank", "image_bank", "templates"} {
		t.Run(key, func(t *testing.T) {
			body := json.RawMessage(`{"` + key + `":{}}`)
			_, err := svc.Put(ctx, body)
			require.Error(t, err)
			var appErr *apperr.AppError
			require.ErrorAs(t, err, &appErr)
			assert.Equal(t, "unsupported settings key: "+key, appErr.Message)
		})
	}
}
