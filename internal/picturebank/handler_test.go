package picturebank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/stretchr/testify/assert"
)

func TestHandlerSetsCachingAndRetryHeaders(t *testing.T) {
	t.Run("metadata cache", func(t *testing.T) {
		handler := NewHandler(&fakePictureService{
			categories: []Category{{ID: "id", Name: "name"}},
		})
		request := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet, "/api/v1/pictures/categories", nil,
		)
		recorder := httptest.NewRecorder()

		err := handler.Categories(recorder, request)

		assert.NoError(t, err)
		assert.Equal(t, "private, max-age=60", recorder.Header().Get("Cache-Control"))
	})

	t.Run("outbound budget", func(t *testing.T) {
		serviceErr := apperr.ErrServiceUnavailable.WithError(ErrRateLimited)
		handler := NewHandler(&fakePictureService{err: serviceErr})
		request := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet, "/api/v1/pictures/categories", nil,
		)
		recorder := httptest.NewRecorder()

		middleware.ErrorMiddleware(handler.Categories).ServeHTTP(recorder, request)

		assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		assert.Equal(t, "1", recorder.Header().Get("Retry-After"))
	})
}

type fakePictureService struct {
	categories []Category
	err        error
}

func (f *fakePictureService) Categories(context.Context) ([]Category, error) {
	return f.categories, f.err
}

func (f *fakePictureService) Search(context.Context, string) ([]Picture, error) {
	return nil, f.err
}

func (f *fakePictureService) Image(context.Context, string) (*Image, error) {
	return nil, f.err
}

func (f *fakePictureService) Import(context.Context, string) (*media.Response, error) {
	return nil, f.err
}
