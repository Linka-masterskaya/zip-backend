package pack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentHandlerListAdaptations(t *testing.T) {
	packID := uuid.New()
	adaptation := Adaptation{
		ID:        uuid.New(),
		PackID:    packID,
		StudentID: uuid.New(),
		Config:    json.RawMessage(`{"settings":{"columns":1}}`),
		CreatedBy: uuid.New(),
	}
	service := &fakeContentService{
		listAdaptationsFn: func(_ context.Context, gotID uuid.UUID) ([]Adaptation, error) {
			assert.Equal(t, packID, gotID)
			return []Adaptation{adaptation}, nil
		},
	}
	handler := NewContentHandler(service)

	rec := performContentAdaptationRequest(
		handler.ListAdaptations,
		"/api/v1/packs/"+packID.String()+"/adaptations",
		packID.String(),
	)

	assert.Equal(t, http.StatusOK, rec.Code)
	var result []Adaptation
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, adaptation.ID, result[0].ID)
	assert.JSONEq(t, string(adaptation.Config), string(result[0].Config))
}

func TestContentHandlerGetAdaptation(t *testing.T) {
	adaptationID := uuid.New()
	adaptation := Adaptation{
		ID:        adaptationID,
		PackID:    uuid.New(),
		StudentID: uuid.New(),
		Config:    json.RawMessage(`{"settings":{"rows":2}}`),
		CreatedBy: uuid.New(),
	}
	service := &fakeContentService{
		getAdaptationFn: func(_ context.Context, gotID uuid.UUID) (*Adaptation, error) {
			assert.Equal(t, adaptationID, gotID)
			return &adaptation, nil
		},
	}
	handler := NewContentHandler(service)

	rec := performContentAdaptationRequest(
		handler.GetAdaptation,
		"/api/v1/adaptations/"+adaptationID.String(),
		adaptationID.String(),
	)

	assert.Equal(t, http.StatusOK, rec.Code)
	var result Adaptation
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.Equal(t, adaptation.ID, result.ID)
	assert.JSONEq(t, string(adaptation.Config), string(result.Config))
}

func TestContentHandlerUpdateAdaptationConfig(t *testing.T) {
	adaptationID := uuid.New()
	config := json.RawMessage(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":2},
		"blocks":[]
	}`)
	service := &fakeContentService{
		updateAdaptationConfigFn: func(
			_ context.Context, gotID uuid.UUID, gotConfig json.RawMessage,
		) (*Adaptation, error) {
			assert.Equal(t, adaptationID, gotID)
			assert.JSONEq(t, string(config), string(gotConfig))
			return &Adaptation{ID: gotID, Config: gotConfig}, nil
		},
	}
	handler := NewContentHandler(service)
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPut,
		"/api/v1/adaptations/"+adaptationID.String()+"/config",
		strings.NewReader(string(config)),
	)
	req.SetPathValue("id", adaptationID.String())
	rec := httptest.NewRecorder()

	middleware.ErrorMiddleware(handler.UpdateAdaptationConfig).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var result Adaptation
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.Equal(t, adaptationID, result.ID)
	assert.JSONEq(t, string(config), string(result.Config))
}

func TestContentHandlerAdaptationsRejectInvalidID(t *testing.T) {
	handler := NewContentHandler(&fakeContentService{})

	for name, method := range map[string]middleware.AppHandler{
		"list":   handler.ListAdaptations,
		"get":    handler.GetAdaptation,
		"update": handler.UpdateAdaptationConfig,
	} {
		t.Run(name, func(t *testing.T) {
			rec := performContentAdaptationRequest(method, "/api/v1/adaptations/invalid", "invalid")
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func performContentAdaptationRequest(
	handler middleware.AppHandler,
	target, id string,
) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	middleware.ErrorMiddleware(handler).ServeHTTP(rec, req)
	return rec
}

type fakeContentService struct {
	contentService

	listAdaptationsFn        func(ctx context.Context, packID uuid.UUID) ([]Adaptation, error)
	getAdaptationFn          func(ctx context.Context, id uuid.UUID) (*Adaptation, error)
	updateAdaptationConfigFn func(ctx context.Context, id uuid.UUID, config json.RawMessage) (*Adaptation, error)
}

func (s *fakeContentService) ListAdaptations(ctx context.Context, packID uuid.UUID) ([]Adaptation, error) {
	if s.listAdaptationsFn != nil {
		return s.listAdaptationsFn(ctx, packID)
	}
	return nil, nil
}

func (s *fakeContentService) GetAdaptation(ctx context.Context, id uuid.UUID) (*Adaptation, error) {
	if s.getAdaptationFn != nil {
		return s.getAdaptationFn(ctx, id)
	}
	return nil, nil
}

func (s *fakeContentService) UpdateAdaptationConfig(ctx context.Context, id uuid.UUID, config json.RawMessage) (*Adaptation, error) {
	if s.updateAdaptationConfigFn != nil {
		return s.updateAdaptationConfigFn(ctx, id, config)
	}
	return nil, nil
}
