package picturebank

import (
	"context"
	"errors"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

// ErrLocalBankNotImplemented keeps the feature flag fail-closed until a local
// adapter is supplied.
var ErrLocalBankNotImplemented = errors.New("local pictures bank is not implemented")

// Source is the read-side boundary shared by picture bank adapters.
type Source interface {
	Categories(context.Context) ([]Category, error)
	Search(context.Context, string) ([]Picture, error)
	Image(context.Context, string) (*Image, error)
}

// NewSource selects the configured adapter. Local mode deliberately fails at
// startup instead of silently contacting the external Pictures Bank.
func NewSource(
	local bool,
	cfg config.PicturesBankConfig,
	limiter distributedLimiter,
) (Source, error) {
	if local {
		return nil, ErrLocalBankNotImplemented
	}
	return NewClient(cfg, limiter)
}
