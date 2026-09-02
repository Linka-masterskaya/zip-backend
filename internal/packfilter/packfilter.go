package packfilter

import "github.com/Linka-masterskaya/zip-backend/internal/apperr"

// Границы возраста повторяют CHECK packs_age_chk.
const (
	MinAge = 3
	MaxAge = 18
)

// ValidateAge проверяет возраст по тем же границам, что и CHECK packs_age_chk.
func ValidateAge(value *int) error {
	return validateAge("age", value)
}

// ValidateAgeFilters проверяет точный возраст и альтернативный диапазон.
func ValidateAgeFilters(age, ageFrom, ageTo *int) error {
	if age != nil && (ageFrom != nil || ageTo != nil) {
		return apperr.ErrBadRequest.WithMessage("age cannot be combined with age_from or age_to")
	}
	if err := ValidateAge(age); err != nil {
		return err
	}
	if err := validateAge("age_from", ageFrom); err != nil {
		return err
	}
	if err := validateAge("age_to", ageTo); err != nil {
		return err
	}
	if ageFrom != nil && ageTo != nil && *ageFrom > *ageTo {
		return apperr.ErrBadRequest.WithMessage("age_from must not be greater than age_to")
	}
	return nil
}

func validateAge(name string, value *int) error {
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
