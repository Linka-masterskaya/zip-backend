package picturebank

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/google/uuid"
)

const (
	localCategoryID   = "local"
	localCategoryName = "Локальный банк"
	localSearchLimit  = 100
)

type localMediaRepository interface {
	GetOrganizationMedia(context.Context, uuid.UUID, uuid.UUID) (*media.File, error)
	ListAccessible(context.Context, uuid.UUID, string, string, int, int) ([]*media.File, error)
}

type localObjectStorage interface {
	GetObject(context.Context, string) (io.ReadCloser, error)
}

// LocalClient exposes organization images from PostgreSQL and private MinIO
// through the same contract as the external Pictures Bank.
type LocalClient struct {
	repo          localMediaRepository
	storage       localObjectStorage
	maxImageBytes int64
}

func NewLocalClient(
	repo localMediaRepository,
	storage localObjectStorage,
	maxImageBytes int64,
) (*LocalClient, error) {
	if repo == nil {
		return nil, errors.New("local pictures bank media repository is required")
	}
	if storage == nil {
		return nil, errors.New("local pictures bank object storage is required")
	}
	if maxImageBytes <= 0 {
		return nil, errors.New("local pictures bank image limit must be positive")
	}
	return &LocalClient{repo: repo, storage: storage, maxImageBytes: maxImageBytes}, nil
}

func (c *LocalClient) Categories(context.Context) ([]Category, error) {
	return []Category{{ID: localCategoryID, Name: localCategoryName}}, nil
}

func (c *LocalClient) Search(ctx context.Context, query string) ([]Picture, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	files, err := c.repo.ListAccessible(
		ctx, userID, "image", strings.TrimSpace(query), localSearchLimit, 0,
	)
	if err != nil {
		return nil, fmt.Errorf("list local pictures: %w", err)
	}
	category := Category{ID: localCategoryID, Name: localCategoryName}
	result := make([]Picture, 0, len(files))
	for _, file := range files {
		name := file.Name
		if name == "" {
			name = file.ID.String()
		}
		result = append(result, Picture{
			ID: file.ID.String(), Name: name, MIMEType: file.MIMEType,
			Categories: []Category{category},
		})
	}
	return result, nil
}

func (c *LocalClient) Image(ctx context.Context, pictureID string) (*Image, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(pictureID)
	if err != nil || id == uuid.Nil {
		return nil, ErrPictureNotFound
	}
	file, err := c.repo.GetOrganizationMedia(ctx, userID, id)
	if errors.Is(err, media.ErrNotFound) {
		return nil, ErrPictureNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get local picture: %w", err)
	}
	if !strings.HasPrefix(file.MIMEType, "image/") || !allowedImageType(file.MIMEType) {
		return nil, ErrPictureNotFound
	}
	if file.SizeBytes > c.maxImageBytes {
		return nil, ErrResponseTooLarge
	}
	reader, err := c.storage.GetObject(ctx, file.MinIOKey)
	if err != nil {
		return nil, fmt.Errorf("%w: open local picture", ErrUnavailable)
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, c.maxImageBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("%w: read local picture", ErrUnavailable)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w: close local picture", ErrUnavailable)
	}
	if int64(len(data)) > c.maxImageBytes {
		return nil, ErrResponseTooLarge
	}
	return &Image{Data: data, ContentType: file.MIMEType}, nil
}
