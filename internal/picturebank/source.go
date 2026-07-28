package picturebank

import (
	"context"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

// Source is the read side shared by the external and local picture banks.
type Source interface {
	Categories(context.Context) ([]Category, error)
	Search(context.Context, string) ([]Picture, error)
	Image(context.Context, string) (*Image, error)
}

// NewSource selects exactly one picture source. Local mode does not initialize
// or call the external Pictures Bank client.
func NewSource(
	local bool,
	cfg config.PicturesBankConfig,
	limiter distributedLimiter,
	repo localMediaRepository,
	storage localObjectStorage,
) (Source, error) {
	if local {
		return NewLocalClient(repo, storage, cfg.MaxImageBytes)
	}
	return NewClient(cfg, limiter)
}
