package settings

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	// MaxDocumentSize limits the persisted JSON document itself. HTTP wrapper
	// overhead (for template name/id fields) is handled separately by Handler.
	MaxDocumentSize = 64 * 1024
	MaxTemplateName = 100
	MaxRequestSize  = MaxDocumentSize + 8*1024

	keyEyeControl      = "eye_control"
	keyCardActivation  = "card_activation"
	keyInteractivity   = "interactivity"
	keyVoice           = "voice"
	keyButtonDirection = "button_direction"
	keyColors          = "colors"
	keyBorderWidth     = "border_width"
)

// allowedTopLevelKeys is the complete AB-48 v1 persistence contract. The
// non-voice values are intentionally opaque JSON in v1: AB-48 requires only
// basic document validation and the project currently has no approved nested
// frontend schema. Adding another top-level key is therefore an explicit API
// contract change rather than an accidental consequence of using JSONB.
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
