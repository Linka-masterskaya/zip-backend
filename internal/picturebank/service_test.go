package picturebank

import (
	"context"
	"errors"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceValidatesInputBeforeCallingUpstream(t *testing.T) {
	client := &fakePictureClient{}
	service := NewService(client)

	_, err := service.Search(t.Context(), " ")
	assertAppStatus(t, err, apperr.ErrBadRequest.HTTPStatus)
	_, err = service.Search(t.Context(), string(make([]rune, 101)))
	assertAppStatus(t, err, apperr.ErrBadRequest.HTTPStatus)
	_, err = service.Image(t.Context(), "not-a-uuid")
	assertAppStatus(t, err, apperr.ErrBadRequest.HTTPStatus)
	assert.Zero(t, client.calls)
}

func TestServiceMapsProtectionErrorsToUnavailable(t *testing.T) {
	for _, clientErr := range []error{
		ErrRateLimited, ErrUnavailable, ErrResponseTooLarge, ErrInvalidResponse,
	} {
		t.Run(clientErr.Error(), func(t *testing.T) {
			service := NewService(&fakePictureClient{err: clientErr})
			_, err := service.Categories(t.Context())
			assertAppStatus(t, err, apperr.ErrServiceUnavailable.HTTPStatus)
		})
	}
}

func TestServiceReturnsReferenceWithoutFetchingOrUploading(t *testing.T) {
	pictureID := uuid.New()
	client := &fakePictureClient{}
	service := NewService(client)

	result, err := service.Import(t.Context(), pictureID.String())

	require.NoError(t, err)
	assert.Equal(t, pictureID, result.SourcePictureID)
	assert.Equal(t, "/api/v1/pictures/"+pictureID.String()+"/content", result.ContentURL)
	assert.Zero(t, client.calls)
}

type fakePictureClient struct {
	err   error
	calls int
	image *Image
}

func (f *fakePictureClient) Categories(context.Context) ([]Category, error) {
	f.calls++
	return nil, f.err
}

func (f *fakePictureClient) Search(context.Context, string) ([]Picture, error) {
	f.calls++
	return nil, f.err
}

func (f *fakePictureClient) Image(context.Context, string) (*Image, error) {
	f.calls++
	return f.image, f.err
}

func (f *fakePictureClient) PicturesByCategory(context.Context, string) ([]Picture, error) {
	f.calls++
	return nil, f.err
}

func assertAppStatus(t *testing.T, err error, status int) {
	t.Helper()
	var appErr *apperr.AppError
	require.Error(t, err)
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, status, appErr.HTTPStatus)
}
