package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/Linka-masterskaya/zip-backend/internal/auth"
	"github.com/Linka-masterskaya/zip-backend/internal/folder"
	"github.com/Linka-masterskaya/zip-backend/internal/health"
	"github.com/Linka-masterskaya/zip-backend/internal/httpapi"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/pack"
	"github.com/Linka-masterskaya/zip-backend/internal/picturebank"
	"github.com/Linka-masterskaya/zip-backend/internal/profile"
	"github.com/Linka-masterskaya/zip-backend/internal/student"
)

type modules struct {
	packs    httpapi.PackHandlers
	media    httpapi.MediaHandlers
	folders  httpapi.FolderHandlers
	students httpapi.StudentHandlers
	auth     httpapi.AuthHandlers
	profile  httpapi.ProfileHandlers
	pictures *picturebank.Handler
	checker  *health.Checker
}

// buildModules wires every domain module on top of the infrastructure.
func buildModules(in *infra) (*modules, error) {
	cfg := in.cfg

	packRepo := pack.NewRepository(in.db)
	packService := pack.NewService(packRepo, in.pub)
	mediaRepo := media.NewRepository(in.db)
	mediaService := media.NewService(mediaRepo, in.storage)

	folderRepo := folder.NewRepository(in.db)
	studentRepo := student.NewRepository(in.db)

	picturesSource, err := picturebank.NewSource(cfg.FeatureFlags.LocalBank, cfg.PicturesBank, in.redis)
	if err != nil {
		return nil, fmt.Errorf("pictures bank source: %w", err)
	}
	picturesService := picturebank.NewService(picturesSource)

	contentService := pack.NewContentService(
		packRepo, in.storage, mediaService, packService,
		func(ctx context.Context, id uuid.UUID) ([]byte, string, error) {
			image, loadErr := picturesService.Image(ctx, id.String())
			if errors.Is(loadErr, picturebank.ErrPictureNotFound) {
				return nil, "", pack.ErrMissingMediaReference
			}
			if loadErr != nil {
				return nil, "", loadErr
			}
			return image.Data, image.ContentType, nil
		},
	)

	authCfg := auth.Config{
		JWTSecret:                cfg.JWT.Secret,
		FrontendURL:              cfg.App.FrontendURL,
		AccessTokenTTL:           cfg.Auth.AccessTokenTTL,
		RefreshTokenTTL:          cfg.Auth.RefreshTokenTTL,
		VerifyEmailTokenTTL:      cfg.Auth.VerifyEmailTokenTTL,
		ResetPasswordTokenTTL:    cfg.Auth.ResetPasswordTokenTTL,
		BcryptCost:               cfg.Auth.BcryptCost,
		RequireEmailVerification: cfg.Auth.RequireEmailVerification,
		CookieSecure:             cfg.Auth.CookieSecure,
		RateLimit:                cfg.RateLimit,
	}
	authService := auth.NewAuthService(auth.NewAuthRepo(in.db), in.redis, in.redis, in.mailer, authCfg, in.crypto)

	profileService := profile.NewService(
		profile.NewRepository(in.db), in.storage, in.mailer, in.crypto, in.redis,
		profile.EmailConfig{
			EmailChangeTTL: cfg.Profile.EmailChangeTTL,
			EmailVerifyTTL: cfg.Profile.EmailVerifyTTL,
		},
	)
	changePasswordService := profile.NewChangePasswordService(profile.NewChangePasswordRepo(in.db), in.redis)

	checker, err := health.NewChecker(in.db, in.redis, in.nc, in.storage, health.PicturesBank{
		Local: cfg.FeatureFlags.LocalBank,
		URL:   cfg.PicturesBank.URL,
	})
	if err != nil {
		return nil, fmt.Errorf("health checker init: %w", err)
	}

	return &modules{
		packs: httpapi.PackHandlers{
			Pack:    pack.NewHandler(packService),
			Content: pack.NewContentHandler(contentService),
		},
		media: httpapi.MediaHandlers{
			Media: media.NewHandler(mediaService),
		},
		folders: httpapi.FolderHandlers{
			Folder: folder.NewHandler(folder.NewService(folderRepo)),
		},
		students: httpapi.StudentHandlers{
			Student: student.NewHandler(student.NewService(studentRepo, in.crypto)),
		},
		auth: httpapi.AuthHandlers{
			Auth: auth.NewHandler(authService, authCfg),
		},
		profile: httpapi.ProfileHandlers{
			Profile:        profile.NewHandler(profileService),
			ChangePassword: profile.NewChangePasswordHandler(changePasswordService),
		},
		pictures: picturebank.NewHandler(picturesService, cfg.PicturesBank.CacheTTL),
		checker:  checker,
	}, nil
}
