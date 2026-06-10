package store

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// =====================================================================
// Verification request history (spec p.84 "Stranica Verifikacija")
// =====================================================================

// RecordVerificationEvent inserts a freshly issued request as
// 'pending'. It is best-effort from the caller's perspective (a failed
// history write must never block issuing a code), so duplicate ids are
// swallowed: a retry of the same issue is a no-op, not a conflict.
func (s *Store) RecordVerificationEvent(ctx context.Context, id, userID, actionKind string) error {
	const q = `
        insert into "user".verification_events (id, user_id, action_kind)
        values ($1, $2, $3)
        on conflict (id) do nothing`
	if _, err := s.DB.Exec(ctx, q, id, userID, actionKind); err != nil {
		logger.From(ctx).ErrorContext(ctx, "record verification event failed", "err", err, "event_id", id, "user_id", userID)
		return apperr.Internal("record verification event", err)
	}
	return nil
}

// ResolveVerificationEvent flips a pending row to its terminal state.
// Only an unresolved ('pending') row is touched, so a late duplicate
// resolve (e.g. a retried consume) can't overwrite the first outcome.
// A missing/already-resolved row is not an error — the history write
// is advisory and may legitimately have never been recorded.
func (s *Store) ResolveVerificationEvent(ctx context.Context, id string, success bool) error {
	status := domain.VerificationFailed
	if success {
		status = domain.VerificationSuccess
	}
	const q = `
        update "user".verification_events
           set status = $2, resolved_at = now()
         where id = $1 and status = 'pending'`
	if _, err := s.DB.Exec(ctx, q, id, status); err != nil {
		logger.From(ctx).ErrorContext(ctx, "resolve verification event failed", "err", err, "event_id", id)
		return apperr.Internal("resolve verification event", err)
	}
	return nil
}

// ListVerificationEvents returns a user's history, newest first,
// capped at limit rows.
func (s *Store) ListVerificationEvents(ctx context.Context, userID string, limit int) ([]domain.VerificationEvent, error) {
	const q = `
        select id, user_id, action_kind, status, created_at, resolved_at
          from "user".verification_events
         where user_id = $1
         order by created_at desc
         limit $2`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, userID, limit)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list verification events failed", "err", err, "user_id", userID)
		return nil, apperr.Internal("list verification events", err)
	}
	defer rows.Close()

	var out []domain.VerificationEvent
	for rows.Next() {
		var e domain.VerificationEvent
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.ActionKind, &e.Status, &e.CreatedAt, &e.ResolvedAt,
		); err != nil {
			logger.From(ctx).ErrorContext(ctx, "scan verification event failed", "err", err, "user_id", userID)
			return nil, apperr.Internal("scan verification event", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		logger.From(ctx).ErrorContext(ctx, "rows verification events failed", "err", err, "user_id", userID)
		return nil, apperr.Internal("rows verification events", err)
	}
	return out, nil
}
