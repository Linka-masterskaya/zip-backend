// Package avatar собирает общие правила загрузки аватара: лимит на файл,
// допустимые типы и разбор ошибок чтения тела. Профиль и картотека
// учеников грузят картинки одинаково, поэтому правила лежат в одном месте.
package avatar

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

const (
	// MaxSizeBytes — предел на саму картинку, MaxBodyBytes добавляет к нему
	// запас на границы multipart.
	MaxSizeBytes      int64 = 2 * 1024 * 1024
	multipartOverhead int64 = 64 * 1024
	MaxBodyBytes      int64 = MaxSizeBytes + multipartOverhead
)

// DetectMIME отсекает всё, что не картинка: банк медиа принимает и звук,
// но аватаром он быть не может.
func DetectMIME(data []byte) string {
	mimeType := http.DetectContentType(data)
	if mimeType == "image/png" || mimeType == "image/jpeg" || mimeType == "image/webp" {
		return mimeType
	}
	return ""
}

// ReadError переводит сбой чтения multipart в ответ API: превышенный лимит
// тела — 413, всё остальное — 400.
func ReadError(err error) error {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) || strings.Contains(err.Error(), "request body too large") {
		return apperr.ErrPayloadTooLarge
	}
	return apperr.ErrBadRequest
}
