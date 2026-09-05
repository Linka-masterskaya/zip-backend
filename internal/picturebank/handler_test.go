package picturebank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
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

	t.Run("deleted picture placeholder", func(t *testing.T) {
		handler := NewHandler(&fakePictureService{err: ErrPictureNotFound})
		request := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet, "/api/v1/pictures/id/content", nil,
		)
		recorder := httptest.NewRecorder()

		err := handler.Image(recorder, request)

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, "image/svg+xml", recorder.Header().Get("Content-Type"))
		assert.Equal(t, "deleted", recorder.Header().Get("X-Picture-Placeholder"))
		assert.Contains(t, recorder.Body.String(), "Картинка удалена")
	})

	t.Run("configured picture cache ttl", func(t *testing.T) {
		handler := NewHandler(&fakePictureService{image: &Image{
			Data: []byte("image"), ContentType: "image/png",
		}}, 90*time.Minute)
		request := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet, "/api/v1/pictures/id/content", nil,
		)
		recorder := httptest.NewRecorder()

		err := handler.Image(recorder, request)

		assert.NoError(t, err)
		assert.Equal(t, "public, max-age=5400", recorder.Header().Get("Cache-Control"))
	})

	t.Run("unavailable picture placeholder", func(t *testing.T) {
		handler := NewHandler(&fakePictureService{err: ErrUnavailable})
		request := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet, "/api/v1/pictures/id/content", nil,
		)
		recorder := httptest.NewRecorder()

		err := handler.Image(recorder, request)

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, "unavailable", recorder.Header().Get("X-Picture-Placeholder"))
		assert.Contains(t, recorder.Body.String(), "Картинка недоступна")
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

	t.Run("pictures by category with DTO", func(t *testing.T) {
		handler := NewHandler(&fakePictureService{
			pictures: []Picture{{
				ID:         "123e4567-e89b-12d3-a456-426614174000",
				Name:       "Троллейбус",
				MIMEType:   "image/png",
				Categories: []Category{{ID: "transport", Name: "Транспорт"}},
			}},
		})
		request := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet, "/api/v1/pictures/category/transport", nil,
		)
		request.SetPathValue("categoryId", "transport")
		recorder := httptest.NewRecorder()

		err := handler.PicturesByCategory(recorder, request)

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, "private, max-age=60", recorder.Header().Get("Cache-Control"))
		assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
		assert.Contains(t, recorder.Body.String(), `"categories":["Транспорт"]`)
		assert.Contains(t, recorder.Body.String(), `"url":"/api/v1/pictures/123e4567-e89b-12d3-a456-426614174000/content"`)
	})
}

type fakePictureService struct {
	categories []Category
	pictures   []Picture
	image      *Image
	err        error
}

func (f *fakePictureService) Categories(context.Context) ([]Category, error) {
	return f.categories, f.err
}

func (f *fakePictureService) Search(context.Context, string) ([]Picture, error) {
	return f.pictures, f.err
}

func (f *fakePictureService) Image(context.Context, string) (*Image, error) {
	return f.image, f.err
}

func (f *fakePictureService) Import(context.Context, string) (*PictureReference, error) {
	return nil, f.err
}

func (f *fakePictureService) PicturesByCategory(context.Context, string) ([]Picture, error) {
	return f.pictures, f.err
}
