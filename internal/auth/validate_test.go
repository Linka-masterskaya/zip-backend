package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateName(t *testing.T) {
	valid := []string{"Анна", "Anna", "Анна Мария", "Анна-Мария", "  Ёжик  ", strings.Repeat("я", 100)}
	invalid := []string{
		"", "   ", "А", "Анна86", "12345", "Анна!?%№;", "Анна.", "-Анна", "Анна-",
		"Анна  Мария", "Анна--Мария", "李明", "Zoë", strings.Repeat("я", 101),
	}
	for _, name := range valid {
		assert.NoError(t, ValidateName("name", name), name)
	}
	for _, name := range invalid {
		err := ValidateName("name", name)
		if assert.Error(t, err, name) {
			assert.Contains(t, err.Error(), "name", name)
		}
	}
}

func TestValidateNewEmail(t *testing.T) {
	valid := []string{
		"user@example.com", "a.b+tag@sub.example.ru", "o'neil@example.org", " user@example.com ",
		strings.Repeat("a", 64) + "@example.com",
	}
	invalid := []string{
		"a@b.c", "почта@example.com", "user@пример.ru", "user@пример.рф",
		strings.Repeat("a", 65) + "@example.com",
		strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 63) + ".com",
		".user@example.com", "user.@example.com", "us..er@example.com",
		"user@-example.com", "user@example", "user@example.c1", "user name@example.com",
	}
	for _, email := range valid {
		assert.NoError(t, ValidateNewEmail(email), email)
	}
	for _, email := range invalid {
		assert.Error(t, ValidateNewEmail(email), email)
	}
}

func TestValidatePasswordRejectsSpacesOnly(t *testing.T) {
	assert.Error(t, ValidatePassword("        "))
	assert.Error(t, ValidatePassword("\t\t\t\t    "))
	assert.NoError(t, ValidatePassword("  pass word  "))
}
