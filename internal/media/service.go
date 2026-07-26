package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/google/uuid"
)

const (
	MaxFileSize = int64(20 * 1024 * 1024)
	readURLTTL  = 15 * time.Minute
)

var (
	ErrNotFound      = errors.New("media not found")
	ErrInUse         = errors.New("media is in use")
	ErrQuotaExceeded = errors.New("organization storage quota exceeded")
)

type File struct {
	ID         uuid.UUID `json:"id"`
	OrgID      uuid.UUID `json:"-"`
	UploaderID uuid.UUID `json:"uploader_id"`
	SHA256     string    `json:"sha256"`
	MIMEType   string    `json:"mime_type"`
	SizeBytes  int64     `json:"size_bytes"`
	MinIOKey   string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}

type Response struct {
	File
	URL string `json:"url"`
}

type repository interface {
	UserOrg(context.Context, uuid.UUID) (uuid.UUID, error)
	Upsert(context.Context, File) (*File, error)
	GetAccessible(context.Context, uuid.UUID, uuid.UUID) (*File, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) (*File, error)
}

type objectStorage interface {
	PutObject(context.Context, string, io.Reader, int64, string) error
	RemoveObject(context.Context, string) error
	PresignedURL(context.Context, string, time.Duration) (string, error)
}

type Service struct {
	repo    repository
	storage objectStorage
}

func NewService(repo repository, storage objectStorage) *Service {
	return &Service{repo: repo, storage: storage}
}

func (s *Service) Upload(ctx context.Context, data []byte) (*Response, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, apperr.ErrBadRequest.WithMessage("media file is empty")
	}
	if int64(len(data)) > MaxFileSize {
		return nil, apperr.ErrPayloadTooLarge
	}
	mimeType := detectMIME(data)
	if mimeType == "" {
		return nil, apperr.ErrBadRequest.WithMessage("media must be png, jpeg, webp, gif, mp3, wav, or ogg")
	}
	orgID, err := s.repo.UserOrg(ctx, userID)
	if err != nil {
		return nil, mediaError(err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	key := path.Join("media", orgID.String(), digest)
	if err = s.storage.PutObject(ctx, key, bytes.NewReader(data), int64(len(data)), mimeType); err != nil {
		return nil, fmt.Errorf("upload media object: %w", err)
	}
	file, err := s.repo.Upsert(ctx, File{
		OrgID: orgID, UploaderID: userID, SHA256: digest,
		MIMEType: mimeType, SizeBytes: int64(len(data)), MinIOKey: key,
	})
	if err != nil {
		if errors.Is(err, ErrQuotaExceeded) {
			if cleanupErr := s.storage.RemoveObject(ctx, key); cleanupErr != nil {
				slog.Warn("remove media rejected by quota", "key", key, "err", cleanupErr)
			}
		}
		return nil, mediaError(err)
	}
	return s.response(ctx, file)
}

func (s *Service) Get(ctx context.Context, mediaID uuid.UUID) (*Response, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	file, err := s.repo.GetAccessible(ctx, userID, mediaID)
	if err != nil {
		return nil, mediaError(err)
	}
	return s.response(ctx, file)
}

func (s *Service) Delete(ctx context.Context, mediaID uuid.UUID) error {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return err
	}
	file, err := s.repo.Delete(ctx, userID, mediaID)
	if err != nil {
		return mediaError(err)
	}
	if err = s.storage.RemoveObject(ctx, file.MinIOKey); err != nil {
		return fmt.Errorf("remove media object: %w", err)
	}
	return nil
}

func (s *Service) response(ctx context.Context, file *File) (*Response, error) {
	url, err := s.storage.PresignedURL(ctx, file.MinIOKey, readURLTTL)
	if err != nil {
		return nil, fmt.Errorf("create media read URL: %w", err)
	}
	return &Response{File: *file, URL: url}, nil
}

func detectMIME(data []byte) string {
	detected := http.DetectContentType(data)
	switch detected {
	case "image/png", "image/jpeg", "image/webp", "image/gif", "audio/mpeg", "audio/wav", "audio/ogg",
		"audio/x-wav", "application/ogg":
		return detected
	default:
		return ""
	}
}

func mediaError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return apperr.ErrNotFound
	}
	if errors.Is(err, ErrQuotaExceeded) {
		return apperr.ErrPayloadTooLarge.WithMessage("organization storage quota exceeded")
	}
	if errors.Is(err, ErrInUse) {
		return apperr.ErrConflict.WithMessage("media is still used by a pack")
	}
	return err
}
