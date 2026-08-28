// Package student implements the defectologist's student registry.
package student

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Student struct {
	ID            uuid.UUID  `json:"id"`
	Email         string     `json:"email"`
	EmailVerified bool       `json:"email_verified"`
	Name          string     `json:"name"`
	Age           *int       `json:"age"`
	Status        string     `json:"status"`
	CardsShift    *string    `json:"cards_shift"`
	LastLessonAt  *time.Time `json:"last_lesson_at"`
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
	LastLessonAt *time.Time `json:"last_lesson_at"`
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
