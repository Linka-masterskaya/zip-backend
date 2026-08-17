package media

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// mediaCursor is a keyset pagination position over (created_at, id).
type mediaCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

func encodeCursor(c mediaCursor) string {
	raw := fmt.Sprintf("%s,%s", c.CreatedAt.UTC().Format(time.RFC3339Nano), c.ID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(value string) (mediaCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return mediaCursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	createdAtRaw, idRaw, ok := strings.Cut(string(raw), ",")
	if !ok {
		return mediaCursor{}, fmt.Errorf("malformed cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtRaw)
	if err != nil {
		return mediaCursor{}, fmt.Errorf("parse cursor time: %w", err)
	}
	id, err := uuid.Parse(idRaw)
	if err != nil {
		return mediaCursor{}, fmt.Errorf("parse cursor id: %w", err)
	}
	return mediaCursor{CreatedAt: createdAt, ID: id}, nil
}
