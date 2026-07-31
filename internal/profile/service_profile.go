package profile

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
)

const maxDisplayNameRunes = 100

var displayNamePattern = regexp.MustCompile(`^[\p{L} ]+$`)

// GetProfile retrieves the full user profile.
func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (*Response, error) {
	// 1. get raw data
	user, err := s.repo.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, profileError(err)
	}

	// 2. decrypt email
	plainEmailBytes, err := s.crypto.Decrypt(user.EncryptedEmail)
	if err != nil {
		slog.Error("failed to decrypt user email", "user_id", userID, logger.Err(err))
		return nil, apperr.ErrInternal.WithMessage("failed to process user data")
	}

	// 3. build a base response
	resp := &Response{
		ID:            user.ID.String(),
		Email:         string(plainEmailBytes),
		Role:          user.Role,
		EmailVerified: user.EmailVerified,
		CreatedAt:     user.CreatedAt,
	}

	// 4. extract nullable fields
	if user.DisplayName.Valid {
		resp.DisplayName = &user.DisplayName.String
	}
	if user.OrgID.Valid {
		resp.OrgID = &user.OrgID.String
	}

	// 5. generate a presigned URL only if the avatar exists
	if user.AvatarKey.Valid && user.AvatarKey.String != "" {
		avatarURL, err := s.storage.PresignedURL(ctx, user.AvatarKey.String, avatarURLTTL)
		if err != nil {
			slog.Warn("failed to generate presigned url for avatar", "key", user.AvatarKey.String, logger.Err(err))
			resp.AvatarURL = nil
		} else {
			resp.AvatarURL = &avatarURL
		}
	}
	return resp, nil
}

// UpdateDisplayName updates the user's display name and returns the full profile.
func (s *Service) UpdateDisplayName(ctx context.Context, userID uuid.UUID, displayName string) (*Response, error) {
	displayName = normalizeDisplayName(displayName)
	if err := validateDisplayName(displayName); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateDisplayName(ctx, userID, displayName); err != nil {
		return nil, profileError(err)
	}
	return s.GetProfile(ctx, userID)
}

func normalizeDisplayName(displayName string) string {
	return strings.Trim(displayName, " ")
}

func validateDisplayName(displayName string) error {
	if displayName == "" {
		return apperr.ErrBadRequest.WithMessage("display_name must not be empty")
	}
	if utf8.RuneCountInString(displayName) > maxDisplayNameRunes {
		return apperr.ErrBadRequest.WithMessage("display_name must not exceed 100 characters")
	}
	if !displayNamePattern.MatchString(displayName) {
		return apperr.ErrBadRequest.WithMessage("display_name must contain only letters and spaces")
	}
	return nil
}
