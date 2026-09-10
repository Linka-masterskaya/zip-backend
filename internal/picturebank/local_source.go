package picturebank

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LocalObjectStorage is the private object-store surface required by local Pictures Bank reads.
type LocalObjectStorage interface {
	GetObject(context.Context, string) (io.ReadCloser, error)
}

// LocalDependencies contains infrastructure used only when local Pictures Bank mode is enabled.
type LocalDependencies struct {
	DB      *pgxpool.Pool
	Storage LocalObjectStorage
}

type localReadRepository interface {
	Categories(context.Context) ([]string, error)
	Search(context.Context, string) ([]localPictureMetadata, error)
	Get(context.Context, uuid.UUID) (*localPictureMetadata, error)
	PicturesByCategory(context.Context, string) ([]localPictureMetadata, error)
}

type localSource struct {
	repo          localReadRepository
	storage       LocalObjectStorage
	maxImageBytes int64
}

func newLocalSource(
	repo localReadRepository,
	objectStorage LocalObjectStorage,
	maxImageBytes int64,
) (*localSource, error) {
	if repo == nil {
		return nil, errors.New("local pictures bank repository is required")
	}
	if objectStorage == nil {
		return nil, errors.New("local pictures bank object storage is required")
	}
	if maxImageBytes <= 0 {
		return nil, errors.New("local pictures bank max image size must be positive")
	}
	return &localSource{repo: repo, storage: objectStorage, maxImageBytes: maxImageBytes}, nil
}

func (s *localSource) Categories(ctx context.Context) ([]Category, error) {
	stored, err := s.repo.Categories(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: local categories: %v", ErrUnavailable, err)
	}
	result := make([]Category, 0, len(stored))
	for _, category := range stored {
		result = append(result, localCategory(category))
	}
	return result, nil
}

func (s *localSource) Search(ctx context.Context, query string) ([]Picture, error) {
	stored, err := s.repo.Search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%w: local search: %v", ErrUnavailable, err)
	}
	result := make([]Picture, 0, len(stored))
	for _, picture := range stored {
		result = append(result, Picture{
			ID:         picture.ID.String(),
			Name:       picture.Title,
			MIMEType:   picture.MIMEType,
			Categories: []Category{localCategory(picture.Category)},
		})
	}
	return result, nil
}

func (s *localSource) Image(ctx context.Context, pictureID string) (*Image, error) {
	id, err := uuid.Parse(pictureID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid local picture id", ErrPictureNotFound)
	}
	picture, err := s.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrPictureNotFound) {
			return nil, ErrPictureNotFound
		}
		return nil, fmt.Errorf("%w: local metadata: %v", ErrUnavailable, err)
	}
	if picture.SizeBytes <= 0 {
		return nil, fmt.Errorf("%w: invalid local picture size", ErrInvalidResponse)
	}
	if picture.SizeBytes > s.maxImageBytes {
		return nil, ErrResponseTooLarge
	}
	if !strings.HasPrefix(picture.MinIOKey, LocalObjectPrefix+"/") {
		return nil, fmt.Errorf("%w: invalid local object namespace", ErrInvalidResponse)
	}

	reader, err := s.storage.GetObject(ctx, picture.MinIOKey)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			return nil, ErrPictureNotFound
		}
		return nil, fmt.Errorf("%w: local object: %v", ErrUnavailable, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, s.maxImageBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("%w: read local object", ErrUnavailable)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w: close local object", ErrUnavailable)
	}
	if int64(len(data)) > s.maxImageBytes {
		return nil, ErrResponseTooLarge
	}
	if int64(len(data)) != picture.SizeBytes {
		return nil, fmt.Errorf("%w: local object size mismatch", ErrInvalidResponse)
	}

	contentType := strings.TrimSpace(strings.Split(picture.MIMEType, ";")[0])
	if !allowedImageType(contentType) {
		contentType = http.DetectContentType(data)
	}
	if !allowedImageType(contentType) {
		return nil, fmt.Errorf("%w: unexpected local image content type", ErrInvalidResponse)
	}
	return &Image{Data: data, ContentType: contentType}, nil
}

func (s *localSource) PicturesByCategory(ctx context.Context, categoryID string) ([]Picture, error) {
	stored, err := s.repo.PicturesByCategory(ctx, categoryID)
	if err != nil {
		return nil, fmt.Errorf("%w: local pictures by category: %v", ErrUnavailable, err)
	}
	result := make([]Picture, 0, len(stored))
	for _, picture := range stored {
		result = append(result, Picture{
			ID:         picture.ID.String(),
			Name:       picture.Title,
			MIMEType:   picture.MIMEType,
			Categories: []Category{localCategory(picture.Category)},
		})
	}
	return result, nil
}

func localCategory(name string) Category {
	return Category{ID: name, Name: name}
}
