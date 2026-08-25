package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateCORSConfig(t *testing.T) {
	valid := CORSConfig{
		AllowOrigins:     []string{"https://example.com"},
		AllowMethods:     []string{"GET", "POST"},
		AllowHeaders:     []string{"Content-Type"},
		AllowCredentials: true,
	}

	t.Run("valid", func(t *testing.T) {
		assert.NoError(t, validateCORSConfig(&valid))
	})

	t.Run("empty origins", func(t *testing.T) {
		cfg := valid
		cfg.AllowOrigins = nil
		assert.ErrorContains(t, validateCORSConfig(&cfg), "cors.allow_origins")
	})

	t.Run("empty methods", func(t *testing.T) {
		cfg := valid
		cfg.AllowMethods = nil
		assert.ErrorContains(t, validateCORSConfig(&cfg), "cors.allow_methods")
	})

	t.Run("empty headers", func(t *testing.T) {
		cfg := valid
		cfg.AllowHeaders = nil
		assert.ErrorContains(t, validateCORSConfig(&cfg), "cors.allow_headers")
	})
}
