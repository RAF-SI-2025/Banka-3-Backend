package service

import "context"

// RunSagaRecoveryTick resumes every saga currently due for recovery and
// returns how many were processed. It is the single-pass form of the
// recovery worker loop: list sagas whose next_attempt_at has passed and
// Resume each. The orchestrator re-takes the per-saga advisory lock, so
// this is safe to fire even if another pass overlaps, and idempotent
// because the bank RPCs dedupe on op_id. No-op when the orchestrator
// isn't wired.
func (s *Service) RunSagaRecoveryTick(ctx context.Context) (int, error) {
	if s.SagaOrch == nil || s.SagaStore == nil {
		return 0, nil
	}
	due, err := s.SagaStore.DueForRecovery(ctx, 100)
	if err != nil {
		s.log().ErrorContext(ctx, "saga recovery: due-row scan failed", "err", err)
		return 0, err
	}
	for _, row := range due {
		if err := s.SagaOrch.Resume(ctx, row.TransactionID); err != nil {
			s.Log.Warn("saga recovery: resume failed",
				"transaction_id", row.TransactionID,
				"saga_type", row.SagaType,
				"err", err.Error())
		}
	}
	return len(due), nil
}
