// Package student implements the defectologist's student registry.
package student

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// dateLayout — формат даты занятия в JSON. Колонка last_lesson_at имеет тип
// DATE, времени в ней нет, поэтому и наружу поле ходит без времени.
const dateLayout = time.DateOnly

// Date — календарная дата. Обычный time.Time сериализуется как RFC3339 и
// добавлял бы к каждой дате занятия бессмысленные 00:00:00Z.
type Date struct {
	time.Time
}

func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Format(dateLayout))
}

func (d *Date) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := time.Parse(dateLayout, raw)
	if err != nil {
		return errors.New("date must be in YYYY-MM-DD format")
	}
	d.Time = parsed
	return nil
}

func dateOf(value *time.Time) *Date {
	if value == nil {
		return nil
	}
	return &Date{Time: *value}
}

func timeOf(value *Date) *time.Time {
	if value == nil {
		return nil
	}
	return &value.Time
}

type Student struct {
	ID            uuid.UUID  `json:"id"`
	Email         string     `json:"email"`
	EmailVerified bool       `json:"email_verified"`
	Name          string     `json:"name"`
	Age           *int       `json:"age"`
	Status        string     `json:"status"`
	CardsShift    *string    `json:"cards_shift"`
	LastLessonAt  *Date      `json:"last_lesson_at"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
	// AvatarURL — presigned-ссылка, выданная на это чтение. Хранить её
	// нельзя: она живёт 15 минут, поэтому в базе лежит avatar_media_id.
	AvatarURL *string    `json:"avatar_url"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"-"`
}

type storedStudent struct {
	ID             uuid.UUID
	EmailEncrypted []byte
	EmailVerified  bool
	Name           string
	Age            *int
	Status         string
	CardsShift     string
	LastLessonAt   *time.Time
	AvatarMediaID  *uuid.UUID
	// AvatarKey — ключ объекта в MinIO, подтянутый join'ом к media_files.
	// Нужен только чтобы выписать presigned-ссылку при отдаче.
	AvatarKey *string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

type CreateInput struct {
	Email         string     `json:"email"`
	Name          string     `json:"name"`
	Age           *int       `json:"age"`
	Status        string     `json:"status"`
	CardsShift    *string    `json:"cards_shift"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
}

type UpdateInput struct {
	Email        *string    `json:"email"`
	Name         *string    `json:"name"`
	Age          *int       `json:"age"`
	Status       *string    `json:"status"`
	LastLessonAt *Date      `json:"last_lesson_at"`
	// CardsShift: отсутствие поля не трогает раскладку, null возвращает
	// её к значению по умолчанию.
	CardsShift nullableField[string] `json:"cards_shift"`
	// AvatarMediaID различает «поле не передали» и «передали null»:
	// null снимает аватар.
	AvatarMediaID nullableField[uuid.UUID] `json:"avatar_media_id"`
}

// nullableField отличает отсутствующее поле JSON от переданного null.
// Обычный указатель их не различает, а для аватара разница смысловая:
// отсутствие — «не трогать», null — «убрать».
type nullableField[T any] struct {
	Set   bool
	Value *T
}

func (f *nullableField[T]) UnmarshalJSON(data []byte) error {
	f.Set = true
	f.Value = nil
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	f.Value = &value
	return nil
}

type storedUpdate struct {
	EmailEncrypted   []byte
	EmailSet         bool
	Name             *string
	Age              *int
	Status           *string
	CardsShift       *string
	LastLessonAt     *time.Time
	LastLessonSet    bool
	AvatarMediaID    *uuid.UUID
	AvatarMediaIDSet bool
}

// avatarResponse отвечает на PUT /students/{id}/avatar. Кроме ссылки
// возвращаем и идентификатор файла: им клиент потом снимает или меняет
// аватар через PATCH.
type avatarResponse struct {
	AvatarURL     *string    `json:"avatar_url"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
}

type ListInput struct {
	SortBy string
	Order  string
	Limit  int
	Offset int
}

type ListResult struct {
	Items []Student `json:"items"`
	Total int       `json:"total"`
}
