package student

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/google/uuid"
)

type repository interface {
	Create(context.Context, uuid.UUID, []byte, CreateInput) (*storedStudent, error)
	List(context.Context, uuid.UUID, ListInput) ([]storedStudent, int, error)
	Update(context.Context, uuid.UUID, uuid.UUID, storedUpdate) (*storedStudent, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
	AvatarMediaAccessible(context.Context, uuid.UUID, uuid.UUID) (bool, error)
}

// objectStorage — часть MinIO, нужная для ссылки на аватар.
type objectStorage interface {
	PresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

type crypto interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}

// avatarURLTTL совпадает с TTL ссылок в профиле и медиа.
const avatarURLTTL = 15 * time.Minute

// defaultCardsShift — раскладка карточек, к которой сводится и отсутствие
// поля при создании, и явный null при обновлении.
const defaultCardsShift = "full"

var errCardsShift = apperr.ErrBadRequest.WithMessage(
	"invalid cards_shift. allowed: left, full, right")

type Service struct {
	repo    repository
	crypto  crypto
	storage objectStorage
}

func NewService(repo repository, crypto crypto, storage objectStorage) *Service {
	return &Service{repo: repo, crypto: crypto, storage: storage}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (*Student, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	normalizeCreate(&input)
	if err = validate(input.Email, input.Name, input.Age, input.Status); err != nil {
		return nil, err
	}
	if !validCardsShift(*input.CardsShift) {
		return nil, errCardsShift
	}
	if err = s.checkAvatarMedia(ctx, ownerID, input.AvatarMediaID); err != nil {
		return nil, err
	}
	encrypted, err := s.crypto.Encrypt([]byte(input.Email))
	if err != nil {
		return nil, fmt.Errorf("student encrypt email: %w", err)
	}
	stored, err := s.repo.Create(ctx, ownerID, encrypted, input)
	if err != nil {
		return nil, mapStudentError(err)
	}
	return s.decode(ctx, stored)
}

func (s *Service) List(ctx context.Context, input ListInput) (*ListResult, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return nil, err
	}

	if input.Limit <= 0 {
		input.Limit = 50
	}
	if input.Limit > 100 {
		input.Limit = 100
	}
	if input.Offset < 0 {
		input.Offset = 0
	}
	if !validSortBy(input.SortBy) {
		return nil, apperr.ErrBadRequest.WithMessage("invalid sort_by. allowed: name, age, status, last_lesson_at")
	}
	if input.Order != "" && strings.ToLower(input.Order) != "asc" && strings.ToLower(input.Order) != "desc" {
		return nil, apperr.ErrBadRequest.WithMessage("invalid order. allowed: asc, desc")
	}

	stored, total, err := s.repo.List(ctx, ownerID, input)
	if err != nil {
		return nil, err
	}

	result := make([]Student, 0, len(stored))
	for index := range stored {
		item, decodeErr := s.decode(ctx, &stored[index])
		if decodeErr != nil {
			return nil, decodeErr
		}
		result = append(result, *item)
	}
	return &ListResult{Items: result, Total: total}, nil
}

func (s *Service) Update(
	ctx context.Context,
	studentID uuid.UUID,
	input UpdateInput,
) (*Student, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	if input.Email == nil && input.Name == nil && input.Age == nil &&
		input.Status == nil && input.LastLessonAt == nil &&
		!input.CardsShift.Set && !input.AvatarMediaID.Set {
		return nil, apperr.ErrBadRequest
	}
	if input.AvatarMediaID.Set {
		if err = s.checkAvatarMedia(ctx, ownerID, input.AvatarMediaID.Value); err != nil {
			return nil, err
		}
	}
	storedInput, err := s.prepareUpdate(input)
	if err != nil {
		return nil, err
	}

	stored, err := s.repo.Update(ctx, ownerID, studentID, storedInput)
	if err != nil {
		return nil, mapStudentError(err)
	}
	return s.decode(ctx, stored)
}

func (s *Service) prepareUpdate(input UpdateInput) (storedUpdate, error) {
	result := storedUpdate{
		Name: input.Name, Age: input.Age, Status: input.Status, LastLessonAt: input.LastLessonAt,
		AvatarMediaID: input.AvatarMediaID.Value, AvatarMediaIDSet: input.AvatarMediaID.Set,
	}
	if input.Name != nil {
		value := strings.TrimSpace(*input.Name)
		input.Name = &value
		result.Name = &value
	}
	if input.Status != nil {
		value := strings.TrimSpace(*input.Status)
		input.Status = &value
		result.Status = &value
	}
	if input.CardsShift.Set {
		value := defaultCardsShift
		if input.CardsShift.Value != nil {
			value = strings.TrimSpace(*input.CardsShift.Value)
		}
		if !validCardsShift(value) {
			return storedUpdate{}, errCardsShift
		}
		result.CardsShift = &value
	}
	if input.Email != nil {
		value := strings.ToLower(strings.TrimSpace(*input.Email))
		if !validEmail(value) {
			return storedUpdate{}, apperr.ErrBadRequest.WithMessage("valid email is required")
		}
		encrypted, err := s.crypto.Encrypt([]byte(value))
		if err != nil {
			return storedUpdate{}, fmt.Errorf("student encrypt email: %w", err)
		}
		result.EmailEncrypted = encrypted
		result.EmailSet = true
	}
	if input.Name != nil && *input.Name == "" {
		return storedUpdate{}, apperr.ErrBadRequest
	}
	if input.Age != nil && (*input.Age < 0 || *input.Age > 100) {
		return storedUpdate{}, apperr.ErrBadRequest
	}
	if input.Status != nil && !validStatus(*input.Status) {
		return storedUpdate{}, apperr.ErrBadRequest
	}
	if input.LastLessonAt != nil {
		today := time.Now().Truncate(24 * time.Hour)
		if input.LastLessonAt.After(today) {
			return storedUpdate{}, apperr.ErrBadRequest.WithMessage("last_lesson_at cannot be in the future")
		}
		result.LastLessonSet = true
	}
	return result, nil
}

func (s *Service) Delete(ctx context.Context, studentID uuid.UUID) error {
	ownerID, err := owner(ctx)
	if err != nil {
		return err
	}
	return mapStudentError(s.repo.Delete(ctx, ownerID, studentID))
}

// checkAvatarMedia отклоняет ссылку на чужой или несуществующий файл до
// записи в базу, чтобы клиент получил внятную ошибку, а не нарушение
// внешнего ключа.
func (s *Service) checkAvatarMedia(ctx context.Context, ownerID uuid.UUID, mediaID *uuid.UUID) error {
	if mediaID == nil {
		return nil
	}
	accessible, err := s.repo.AvatarMediaAccessible(ctx, ownerID, *mediaID)
	if err != nil {
		return err
	}
	if !accessible {
		return apperr.ErrBadRequest.WithMessage("avatar_media_id is unknown or not accessible")
	}
	return nil
}

func (s *Service) decode(ctx context.Context, stored *storedStudent) (*Student, error) {
	email, err := s.crypto.Decrypt(stored.EmailEncrypted)
	if err != nil {
		return nil, fmt.Errorf("student decrypt email: %w", err)
	}
	cardsShift := stored.CardsShift
	return &Student{
		ID: stored.ID, Email: string(email), EmailVerified: stored.EmailVerified,
		Name: stored.Name, Age: stored.Age, Status: stored.Status,
		CardsShift: &cardsShift, LastLessonAt: stored.LastLessonAt,
		AvatarMediaID: stored.AvatarMediaID, AvatarURL: s.avatarURL(ctx, stored),
		CreatedAt: stored.CreatedAt, UpdatedAt: stored.UpdatedAt,
	}, nil
}

// avatarURL выписывает presigned-ссылку на аватар. Сбой подписи не должен
// ронять чтение картотеки: список учеников важнее картинки, поэтому
// возвращаем nil и пишем в лог.
func (s *Service) avatarURL(ctx context.Context, stored *storedStudent) *string {
	if stored.AvatarKey == nil || *stored.AvatarKey == "" || s.storage == nil {
		return nil
	}
	url, err := s.storage.PresignedURL(ctx, *stored.AvatarKey, avatarURLTTL)
	if err != nil {
		slog.WarnContext(ctx, "student avatar url", "student_id", stored.ID, "err", err)
		return nil
	}
	return &url
}

func owner(ctx context.Context) (uuid.UUID, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	role, err := authctx.RoleFromCtx(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if role != "defectologist" && role != "head_defectologist" && role != "admin" {
		return uuid.Nil, apperr.ErrForbidden
	}
	return userID, nil
}

func normalizeCreate(input *CreateInput) {
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Name = strings.TrimSpace(input.Name)
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "active"
	}
	value := defaultCardsShift
	if input.CardsShift != nil {
		value = strings.TrimSpace(*input.CardsShift)
	}
	input.CardsShift = &value
}

func validate(email, name string, age *int, status string) error {
	if !validEmail(email) || name == "" || !validStatus(status) {
		return apperr.ErrBadRequest
	}
	if age != nil && (*age < 0 || *age > 100) {
		return apperr.ErrBadRequest
	}
	return nil
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}

func validCardsShift(value string) bool {
	return value == "left" || value == "full" || value == "right"
}

func validStatus(value string) bool {
	return value == "active" || value == "paused" ||
		value == "archived" || value == "one_time"
}

func mapStudentError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperr.ErrNotFound
	case errors.Is(err, ErrHasFolder):
		return apperr.ErrConflict
	default:
		return err
	}
}

func validSortBy(field string) bool {
	switch field {
	case "", "name", "age", "status", "last_lesson_at":
		return true
	default:
		return false
	}
}
