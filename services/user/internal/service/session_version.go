package service

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// GetSessionVersion returns the user's current session_version. Used by
// the gateway middleware on Redis cache miss.
func (s *Service) GetSessionVersion(ctx context.Context, kind domain.UserKind, userID string) (int64, error) {
	switch kind {
	case domain.KindEmployee:
		e, err := s.Store.GetEmployeeByID(ctx, userID)
		if err != nil {
			return 0, err
		}
		return e.SessionVersion, nil
	case domain.KindClient:
		c, err := s.Store.GetClientByID(ctx, userID)
		if err != nil {
			return 0, err
		}
		return c.SessionVersion, nil
	}
	return 0, apperr.Internal("unknown user kind", nil)
}
