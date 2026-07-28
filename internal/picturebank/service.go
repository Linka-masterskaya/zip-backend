package picturebank

import (
	"context"
	"errors"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/google/uuid"
)

type Service struct {
	client   Source
	uploader mediaUploader
}

type mediaUploader interface {
	Upload(context.Context, []byte) (*media.Response, error)
}

func NewService(client Source, uploader mediaUploader) *Service {
	return &Service{client: client, uploader: uploader}
}

func (s *Service) Categories(ctx context.Context) ([]Category, error) {
	result, err := s.client.Categories(ctx)
	return result, pictureBankError(err)
}

func (s *Service) Search(ctx context.Context, query string) ([]Picture, error) {
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 100 {
		return nil, apperr.ErrBadRequest.WithMessage("query must contain between 1 and 100 characters")
	}
	result, err := s.client.Search(ctx, query)
	return result, pictureBankError(err)
}

func (s *Service) Image(ctx context.Context, pictureID string) (*Image, error) {
	if _, err := uuid.Parse(pictureID); err != nil {
		return nil, apperr.ErrBadRequest.WithMessage("picture id must be a valid UUID")
	}
	result, err := s.client.Image(ctx, pictureID)
	return result, pictureBankError(err)
}

func (s *Service) Import(ctx context.Context, pictureID string) (*media.Response, error) {
	if _, err := uuid.Parse(pictureID); err != nil {
		return nil, apperr.ErrBadRequest.WithMessage("picture id must be a valid UUID")
	}
	image, err := s.client.Image(ctx, pictureID)
	if err != nil {
		return nil, pictureBankError(err)
	}
	result, err := s.uploader.Upload(ctx, image.Data)
	return result, err
}

func pictureBankError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrPictureNotFound):
		return apperr.ErrNotFound.WithMessage("picture not found")
	case errors.Is(err, ErrRateLimited):
		return apperr.ErrServiceUnavailable.
			WithMessage("Pictures Bank request budget is temporarily exhausted").
			WithError(err)
	case errors.Is(err, ErrUnavailable):
		return apperr.ErrServiceUnavailable.
			WithMessage("Pictures Bank is temporarily unavailable").
			WithError(err)
	case errors.Is(err, ErrResponseTooLarge), errors.Is(err, ErrInvalidResponse):
		return apperr.ErrServiceUnavailable.
			WithMessage("Pictures Bank returned an invalid response").
			WithError(err)
	default:
		return err
	}
}
