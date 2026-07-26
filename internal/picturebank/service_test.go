package picturebank

import (
	"context"
	"errors"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceValidatesInputBeforeCallingUpstream(t *testing.T) {
	client := &fakePictureClient{}
	service := NewService(client, &fakeMediaUploader{})

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
			service := NewService(&fakePictureClient{err: clientErr}, &fakeMediaUploader{})
			_, err := service.Categories(t.Context())
			assertAppStatus(t, err, apperr.ErrServiceUnavailable.HTTPStatus)
		})
	}
}

func TestServiceImportsPictureThroughMediaPipeline(t *testing.T) {
	pictureID := uuid.New()
	imageData := []byte("image")
	uploader := &fakeMediaUploader{
		uploadFn: func(_ context.Context, data []byte) (*media.Response, error) {
			assert.Equal(t, imageData, data)
			return &media.Response{File: media.File{ID: uuid.New()}}, nil
		},
	}
	service := NewService(
		&fakePictureClient{image: &Image{Data: imageData, ContentType: "image/png"}},
		uploader,
	)

	result, err := service.Import(t.Context(), pictureID.String())

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, result.ID)
	assert.Equal(t, 1, uploader.calls)
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

type fakeMediaUploader struct {
	uploadFn func(context.Context, []byte) (*media.Response, error)
	calls    int
}

func (f *fakeMediaUploader) Upload(
	ctx context.Context,
	data []byte,
) (*media.Response, error) {
	f.calls++
	if f.uploadFn != nil {
		return f.uploadFn(ctx, data)
	}
	return &media.Response{}, nil
}

func assertAppStatus(t *testing.T, err error, status int) {
	t.Helper()
	var appErr *apperr.AppError
	require.Error(t, err)
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, status, appErr.HTTPStatus)
}
