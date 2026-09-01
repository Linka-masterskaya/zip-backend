package profile

import (
	"context"
	"errors"
	"fmt"
)

// DeleteProfile first installs a Redis deletion barrier that atomically blocks
// creation of new login sessions and advances sess_ver, then commits the
// authoritative database soft-delete. If the database step fails, new logins
// are re-enabled while the advanced session version is intentionally retained.
// This ordering closes the delete-vs-login race without making PostgreSQL and
// Redis part of one distributed transaction.
func (s *Service) DeleteProfile(ctx context.Context, userID string) error {
	if s.sessions == nil {
		return fmt.Errorf("delete profile: session manager is not configured")
	}
	if err := s.sessions.DisableUserSessions(ctx, userID); err != nil {
		return fmt.Errorf("delete profile: disable sessions: %w", err)
	}

	change, err := s.repo.SoftDeleteUser(ctx, userID)
	if err != nil {
		if enableErr := s.sessions.EnableUserSessions(ctx, userID); enableErr != nil {
			return errors.Join(
				profileError(err),
				fmt.Errorf("re-enable sessions after failed soft-delete: %w", enableErr),
			)
		}
		return profileError(err)
	}

	s.cleanupDeletedAvatar(ctx, userID, change)
	return nil
}
