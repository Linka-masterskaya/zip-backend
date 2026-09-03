package picturebank

import (
	"context"
	"errors"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

// Source is the read-side boundary shared by picture bank adapters.
type Source interface {
	Categories(context.Context) ([]Category, error)
	Search(context.Context, string) ([]Picture, error)
	Image(context.Context, string) (*Image, error)
	PicturesByCategory(context.Context, string) ([]Picture, error)
}

// NewSource selects the configured adapter without initializing the unused one.
func NewSource(
	local bool,
	cfg config.PicturesBankConfig,
	limiter distributedLimiter,
	localDependencies ...LocalDependencies,
) (Source, error) {
	if !local {
		return NewClient(cfg, limiter)
	}
	if len(localDependencies) != 1 {
		return nil, errors.New("local pictures bank dependencies are required")
	}
	dependencies := localDependencies[0]
	if dependencies.DB == nil {
		return nil, errors.New("local pictures bank database is required")
	}
	return newLocalSource(
		newLocalRepository(dependencies.DB),
		dependencies.Storage,
		cfg.MaxImageBytes,
	)
}
