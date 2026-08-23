package settings

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	// MaxDocumentSize limits the persisted JSON document itself. HTTP wrapper
	// overhead (for template name/id fields) is handled separately by Handler.
	MaxDocumentSize     = 64 * 1024
	MaxTemplateName     = 100
	MaxTemplatesPerUser = 100
	MaxBorderWidth      = 32
	MaxRequestSize      = MaxDocumentSize + 8*1024

	keyEyeControl      = "eye_control"
	keyCardActivation  = "card_activation"
	keyInteractivity   = "interactivity"
	keyVoice           = "voice"
	keyButtonDirection = "button_direction"
	keyColors          = "colors"
	keyBorderWidth     = "border_width"
)

// allowedTopLevelKeys is the complete AB-48 v1 persistence contract. Colors
// and border_width have a minimal CSS-safe nested contract; the other non-voice
// values remain opaque JSON until their frontend schema is approved. Adding a
// top-level key is an explicit API contract change rather than an accidental
// consequence of using JSONB.
var allowedTopLevelKeys = map[string]struct{}{
	keyEyeControl:      {},
	keyCardActivation:  {},
	keyInteractivity:   {},
	keyVoice:           {},
	keyButtonDirection: {},
	keyColors:          {},
	keyBorderWidth:     {},
}

type Template struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Body      json.RawMessage `json:"body"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type CreateTemplateRequest struct {
	Name string          `json:"name"`
	Body json.RawMessage `json:"body"`
}
