package httpquery

import (
	"net/http"
	"strconv"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
)

// Int возвращает 0 для отсутствующего параметра: вызывающий код сам решает,
// что считать значением по умолчанию.
func Int(r *http.Request, name string) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apperr.ErrBadRequest.WithMessage(name + " must be an integer")
	}
	return value, nil
}

// OptionalInt отличает отсутствующий параметр от нуля, поэтому возвращает
// указатель.
func OptionalInt(r *http.Request, name string) (*int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, apperr.ErrBadRequest.WithMessage(name + " must be an integer")
	}
	return &value, nil
}

// OptionalUUID разбирает необязательный идентификатор. Нулевой uuid считаем
// мусором: он никогда не адресует настоящую запись.
func OptionalUUID(r *http.Request, name string) (*uuid.UUID, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	value, err := uuid.Parse(raw)
	if err != nil || value == uuid.Nil {
		return nil, apperr.ErrBadRequest.WithMessage(name + " must be a UUID")
	}
	return &value, nil
}
