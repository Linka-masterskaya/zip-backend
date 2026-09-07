package picturebank

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// LocalObjectPrefix keeps system Pictures Bank blobs separate from user media.
	LocalObjectPrefix  = "system/pictures-bank"
	maxSeedTextRunes   = 200
	seedCleanupTimeout = 10 * time.Second
)

// LocalAdminStorage is the private object-store surface used by the seed command.
type LocalAdminStorage interface {
	LocalObjectStorage
	PutObject(context.Context, string, io.Reader, int64, string) error
	RemoveObject(context.Context, string) error
}

// SeedInput describes one system image to add to the local Pictures Bank.
type SeedInput struct {
	ID       uuid.UUID
	Category string
	Title    string
	Data     []byte
}

type localAdminRepository interface {
	Get(context.Context, uuid.UUID) (*localPictureMetadata, error)
	Create(context.Context, localPictureMetadata) error
	Delete(context.Context, uuid.UUID) error
}

// Seeder manages system Pictures Bank content outside the public HTTP API.
type Seeder struct {
	repo          localAdminRepository
	storage       LocalAdminStorage
	maxImageBytes int64
}

// NewSeeder creates the supported local Pictures Bank ingestion utility.
func NewSeeder(
	pool *pgxpool.Pool,
	objectStorage LocalAdminStorage,
	maxImageBytes int64,
) (*Seeder, error) {
	if pool == nil {
		return nil, errors.New("local pictures bank database is required")
	}
	if objectStorage == nil {
		return nil, errors.New("local pictures bank object storage is required")
	}
	if maxImageBytes <= 0 {
		return nil, errors.New("local pictures bank max image size must be positive")
	}
	return &Seeder{
		repo:          newLocalRepository(pool),
		storage:       objectStorage,
		maxImageBytes: maxImageBytes,
	}, nil
}

// Add validates and stores one system picture under the reserved MinIO prefix.
func (s *Seeder) Add(ctx context.Context, input SeedInput) (uuid.UUID, error) {
	category, err := validateSeedText("category", input.Category)
	if err != nil {
		return uuid.Nil, err
	}
	title, err := validateSeedText("title", input.Title)
	if err != nil {
		return uuid.Nil, err
	}
	if len(input.Data) == 0 {
		return uuid.Nil, errors.New("picture file is empty")
	}
	if int64(len(input.Data)) > s.maxImageBytes {
		return uuid.Nil, fmt.Errorf("picture exceeds maximum size of %d bytes", s.maxImageBytes)
	}
	mimeType := http.DetectContentType(input.Data)
	if !allowedImageType(mimeType) {
		return uuid.Nil, fmt.Errorf("unsupported picture type %q", mimeType)
	}

	id := input.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	key := path.Join(LocalObjectPrefix, id.String())
	metadata := localPictureMetadata{
		ID: id, Category: category, Title: title, MIMEType: mimeType,
		SizeBytes: int64(len(input.Data)), MinIOKey: key,
	}

	// Reserve the UUID in PostgreSQL before touching MinIO. This prevents a
	// duplicate explicit ID from overwriting an existing object and then
	// deleting it when the metadata insert reports the conflict.
	if err = s.repo.Create(ctx, metadata); err != nil {
		return uuid.Nil, err
	}

	if err = s.storage.PutObject(
		ctx,
		key,
		bytes.NewReader(input.Data),
		int64(len(input.Data)),
		mimeType,
	); err == nil {
		return id, nil
	}

	storeErr := fmt.Errorf("store local picture object %s: %w", id, err)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), seedCleanupTimeout)
	defer cancel()

	// PutObject may fail after the server has accepted the object (for example,
	// when the response is lost). Remove the reserved key before deleting
	// metadata so an ambiguous write cannot leave an invisible orphan.
	if cleanupErr := s.storage.RemoveObject(cleanupCtx, key); cleanupErr != nil {
		return uuid.Nil, errors.Join(
			storeErr,
			fmt.Errorf("remove possible object after write failure for %s: %w", id, cleanupErr),
		)
	}
	if cleanupErr := s.repo.Delete(cleanupCtx, id); cleanupErr != nil {
		return uuid.Nil, errors.Join(
			storeErr,
			fmt.Errorf("remove metadata after object failure for %s: %w", id, cleanupErr),
		)
	}
	return uuid.Nil, storeErr
}

// Delete removes one system picture from MinIO and then its metadata row.
func (s *Seeder) Delete(ctx context.Context, id uuid.UUID) error {
	picture, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(picture.MinIOKey, LocalObjectPrefix+"/") {
		return fmt.Errorf("refusing to delete object outside %q", LocalObjectPrefix)
	}
	if err = s.storage.RemoveObject(ctx, picture.MinIOKey); err != nil {
		return fmt.Errorf("remove local picture object: %w", err)
	}
	if err = s.repo.Delete(ctx, id); err != nil {
		return err
	}
	return nil
}

func validateSeedText(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s must be valid UTF-8", field)
	}
	if utf8.RuneCountInString(value) > maxSeedTextRunes {
		return "", fmt.Errorf("%s must not exceed %d characters", field, maxSeedTextRunes)
	}
	return value, nil
}
