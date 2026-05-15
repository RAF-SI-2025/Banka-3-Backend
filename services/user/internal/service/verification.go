package service

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// Verification history (spec p.84). These are thin pass-throughs: the
// gateway owns verification semantics (code TTL, attempt budget, the
// pending→expired projection); the user service only durably stores
// the request's existence and terminal outcome so it outlives the
// Redis code and a restart.

// defaultHistoryLimit caps an unbounded ListVerificationHistory call.
// historyLimitMax is the hard ceiling regardless of what the caller
// asks for, so a client can't request an unbounded scan.
const (
	defaultHistoryLimit = 50
	historyLimitMax     = 200
)

func (s *Service) RecordVerificationEvent(ctx context.Context, id, userID, actionKind string) error {
	return s.Store.RecordVerificationEvent(ctx, id, userID, actionKind)
}

func (s *Service) ResolveVerificationEvent(ctx context.Context, id string, success bool) error {
	return s.Store.ResolveVerificationEvent(ctx, id, success)
}

func (s *Service) ListVerificationHistory(ctx context.Context, userID string, limit int) ([]domain.VerificationEvent, error) {
	switch {
	case limit <= 0:
		limit = defaultHistoryLimit
	case limit > historyLimitMax:
		limit = historyLimitMax
	}
	return s.Store.ListVerificationEvents(ctx, userID, limit)
}
