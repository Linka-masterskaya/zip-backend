// Package packfilter хранит правила фильтрации наборов. Их проверяет и
// листинг наборов, и содержимое разделов, поэтому границы и формулировки
// ошибок должны совпадать.
package packfilter

import "github.com/Linka-masterskaya/zip-backend/internal/apperr"

// Границы возраста повторяют CHECK'и packs_age_min_chk и packs_age_max_chk.
const (
	MinAge = 3
	MaxAge = 18
)

// ValidateAge принимает имя параметра, чтобы одна и та же проверка годилась
// и для age в фильтре, и для age_min/age_max в метаданных набора.
func ValidateAge(name string, value *int) error {
	if value == nil {
		return nil
	}
	if *value < MinAge || *value > MaxAge {
		return apperr.ErrBadRequest.WithMessage(name + " must be between 3 and 18")
	}
	return nil
}

func ValidateDifficulty(value string) error {
	if value == "" || Valid(value) {
		return nil
	}
	return apperr.ErrBadRequest.WithMessage("difficulty must be easy, medium, or hard")
}

func Valid(value string) bool {
	return value == "easy" || value == "medium" || value == "hard"
}
