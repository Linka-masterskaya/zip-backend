package packfilter

import "github.com/Linka-masterskaya/zip-backend/internal/apperr"

// Границы возраста повторяют CHECK packs_age_chk.
const (
	MinAge = 3
	MaxAge = 18
)

// ValidateAge проверяет возраст по тем же границам, что и CHECK packs_age_chk.
func ValidateAge(value *int) error {
	if value == nil {
		return nil
	}
	if *value < MinAge || *value > MaxAge {
		return apperr.ErrBadRequest.WithMessage("age must be between 3 and 18")
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
