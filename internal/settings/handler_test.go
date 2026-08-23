package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSettingsService struct {
	getFn            func(context.Context) (json.RawMessage, error)
	putFn            func(context.Context, json.RawMessage) (json.RawMessage, error)
	listTemplatesFn  func(context.Context) ([]Template, error)
	createTemplateFn func(context.Context, string, json.RawMessage) (*Template, error)
	deleteTemplateFn func(context.Context, uuid.UUID) error
}

func (f *fakeSettingsService) Get(ctx context.Context) (json.RawMessage, error) {
	return f.getFn(ctx)
}
func (f *fakeSettingsService) Put(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	return f.putFn(ctx, body)
}
func (f *fakeSettingsService) ListTemplates(ctx context.Context) ([]Template, error) {
	return f.listTemplatesFn(ctx)
}
func (f *fakeSettingsService) CreateTemplate(ctx context.Context, name string, body json.RawMessage) (*Template, error) {
	return f.createTemplateFn(ctx, name, body)
}
func (f *fakeSettingsService) DeleteTemplate(ctx context.Context, id uuid.UUID) error {
	return f.deleteTemplateFn(ctx, id)
}

func TestHandlerGetAndPut(t *testing.T) {
	body := json.RawMessage(`{"voice":"alena"}`)
	svc := &fakeSettingsService{
		getFn: func(context.Context) (json.RawMessage, error) { return body, nil },
		putFn: func(_ context.Context, got json.RawMessage) (json.RawMessage, error) {
			assert.JSONEq(t, string(body), string(got))
			return got, nil
		},
	}
	h := NewHandler(svc)

	for _, tc := range []struct {
		name   string
		method string
		call   func(http.ResponseWriter, *http.Request) error
	}{
		{name: "get", method: http.MethodGet, call: h.Get},
		{name: "put", method: http.MethodPut, call: h.Put},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reqBody *bytes.Reader
			if tc.method == http.MethodPut {
				reqBody = bytes.NewReader(body)
			} else {
				reqBody = bytes.NewReader(nil)
			}
			req := httptest.NewRequestWithContext(context.Background(), tc.method, "/api/v1/settings", reqBody)
			rec := httptest.NewRecorder()
			require.NoError(t, tc.call(rec, req))
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.JSONEq(t, string(body), rec.Body.String())
		})
	}
}

func TestHandlerPutReturnsStoredRepresentation(t *testing.T) {
	requestBody := json.RawMessage(`{ "border_width": 2 }`)
	storedBody := json.RawMessage(`{"border_width": 2}`)
	h := NewHandler(&fakeSettingsService{
		putFn: func(_ context.Context, got json.RawMessage) (json.RawMessage, error) {
			assert.Equal(t, string(requestBody), string(got))
			return storedBody, nil
		},
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/settings", bytes.NewReader(requestBody))
	rec := httptest.NewRecorder()

	require.NoError(t, h.Put(rec, req))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, string(storedBody)+"\n", rec.Body.String())
}

func TestHandlerPutRejectsOversizedRequest(t *testing.T) {
	h := NewHandler(&fakeSettingsService{
		putFn: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("service must not be called")
			return nil, nil
		},
	})
	body := `{"colors":"` + strings.Repeat("x", MaxRequestSize+1) + `"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/settings", strings.NewReader(body))
	rec := httptest.NewRecorder()

	err := h.Put(rec, req)
	require.Error(t, err)
	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusRequestEntityTooLarge, appErr.HTTPStatus)
}

func TestHandlerCreateTemplateDisallowsUnknownWrapperFields(t *testing.T) {
	h := NewHandler(&fakeSettingsService{
		createTemplateFn: func(context.Context, string, json.RawMessage) (*Template, error) {
			t.Fatal("service must not be called")
			return nil, nil
		},
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/settings/templates",
		strings.NewReader(`{"name":"A","body":{},"extra":true}`))
	rec := httptest.NewRecorder()

	err := h.CreateTemplate(rec, req)
	require.Error(t, err)
	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPStatus)
}

func TestHandlerCreateTemplateRejectsTrailingJSON(t *testing.T) {
	h := NewHandler(&fakeSettingsService{
		createTemplateFn: func(context.Context, string, json.RawMessage) (*Template, error) {
			t.Fatal("service must not be called")
			return nil, nil
		},
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/settings/templates",
		strings.NewReader(`{"name":"A","body":{}} {"name":"B","body":{}}`))
	rec := httptest.NewRecorder()

	err := h.CreateTemplate(rec, req)
	require.Error(t, err)
	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPStatus)
}

func TestHandlerDeleteTemplateUsesPathID(t *testing.T) {
	id := uuid.New()
	called := false
	h := NewHandler(&fakeSettingsService{
		deleteTemplateFn: func(_ context.Context, got uuid.UUID) error {
			called = true
			assert.Equal(t, id, got)
			return nil
		},
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/settings/templates/"+id.String(), nil)
	req.SetPathValue("id", id.String())
	rec := httptest.NewRecorder()

	require.NoError(t, h.DeleteTemplate(rec, req))
	assert.True(t, called)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}
