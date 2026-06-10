// Inter-bank retry queue (celina 5 — todoSpec "Retry queue").
//
// When a partner bank is unavailable mid cross-bank payment, the saga
// parks (status=running, transient error) instead of failing. The
// SubmitCrossBankPayment path then enqueues one retry entry here. A
// worker re-drives the parked saga every 5s; after 30s without success
// it aborts (rolls the saga back) and notifies the client the
// transaction failed.
//
// Re-attempt mechanism: the entry references the saga transaction_id;
// retrying just resumes the saga via the orchestrator (the saga steps
// are idempotent, so a re-fire after the partner recovers is safe). We
// read the post-resume saga status to decide the entry's fate:
//   * completed              → succeeded
//   * still running (parked) → reschedule +5s (unless past deadline)
//   * compensated / failed   → failed (the saga itself gave up)
//
// The 5s/30s cadence is fixed by the spec; the worker interval (5s) is
// the scheduler's, while the 30s deadline is anchored to the entry's
// created_at so re-enqueues never extend the window.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
)

const (
	// interbankRetryInterval is the spec's 5s re-attempt cadence.
	interbankRetryInterval = 5 * time.Second
	// interbankRetryDeadline is the spec's 30s give-up window.
	interbankRetryDeadline = 30 * time.Second
)

// enqueueInterbankRetry records a parked cross-bank payment for retry.
// Called from the SubmitCrossBankPayment path when the saga ends up
// parked (partner unavailable). Best-effort: a queueing failure is
// logged but never fails the originating request (the saga is already
// persisted and the recovery worker is a backstop). Idempotent on the
// saga transaction_id.
func (s *Service) enqueueInterbankRetry(ctx context.Context, txID, partnerBankCode, userID string, userKind domain.UserKind) {
	if s.Store == nil {
		return
	}
	now := s.now()
	_, err := s.Store.EnqueueInterbankRetry(ctx, &domain.InterbankRetryEntry{
		TransactionID:   txID,
		PartnerBankCode: partnerBankCode,
		Operation:       crossBankPaymentSagaType,
		UserID:          userID,
		UserKind:        userKind,
		NextRetryAt:     now.Add(interbankRetryInterval),
		DeadlineAt:      now.Add(interbankRetryDeadline),
	})
	if err != nil {
		s.Log.Warn("interbank retry: enqueue failed",
			"transaction_id", txID, "err", err.Error())
	}
}

// RunInterbankRetryTick re-drives every pending retry entry that is due.
// Driven by the scheduler's `trading-interbank-retry` job (5s interval).
// Returns the number of entries that reached a terminal state this tick
// (succeeded + failed) for log visibility.
//
// Per entry:
//   - resume the parked saga (idempotent re-fire)
//   - if the saga completed → mark succeeded
//   - if the saga is terminal-failed → mark failed + notify client
//   - else if now > deadline_at → roll the saga back, mark failed + notify
//   - else → reschedule next_retry_at = now + 5s
func (s *Service) RunInterbankRetryTick(ctx context.Context) (int, error) {
	if s.SagaOrch == nil || s.SagaStore == nil {
		return 0, nil
	}
	now := s.now()
	entries, err := s.Store.ListDueInterbankRetries(ctx, now)
	if err != nil {
		s.log().ErrorContext(ctx, "interbank retry: due-entry scan failed", "err", err)
		return 0, err
	}
	settled := 0
	for _, e := range entries {
		if s.processInterbankRetry(ctx, e, now) {
			settled++
		}
	}
	return settled, nil
}

// retryAction is the decision the retry state machine makes for one
// entry after a saga re-fire: keep retrying, succeed, or give up.
type retryAction int

const (
	// retryReschedule — saga still parked, deadline not reached: arm +5s.
	retryReschedule retryAction = iota
	// retrySucceed — saga completed.
	retrySucceed
	// retryExpire — saga terminal-failed OR the 30s deadline passed:
	// abort (release the local reservation) + fail + notify.
	retryExpire
)

// decideRetryAction is the pure 5s/30s state machine: given a saga
// status (empty string = unreadable) and the entry's deadline relative
// to `now`, it returns what the worker should do. Kept side-effect-free
// so the retry semantics are unit-testable without a DB or live saga.
func decideRetryAction(sagaStatus saga.Status, deadlineAt, now time.Time) retryAction {
	switch sagaStatus {
	case saga.StatusCompleted:
		return retrySucceed
	case saga.StatusFailed, saga.StatusCompensated:
		// The saga itself gave up — give up too, regardless of deadline.
		return retryExpire
	default:
		// Still running / parked / unreadable: keep retrying until the 30s
		// window elapses, then abort.
		if !now.Before(deadlineAt) {
			return retryExpire
		}
		return retryReschedule
	}
}

// processInterbankRetry handles one due entry. Returns true when the
// entry reached a terminal state (succeeded/failed) this tick.
func (s *Service) processInterbankRetry(ctx context.Context, e *domain.InterbankRetryEntry, now time.Time) bool {
	// Re-fire the saga. Resume is idempotent and lock-guarded; a transient
	// failure leaves the row at status=running (parked again).
	if err := s.SagaOrch.Resume(ctx, e.TransactionID); err != nil {
		s.Log.Debug("interbank retry: resume returned error (will re-check status)",
			"transaction_id", e.TransactionID, "err", err.Error())
	}

	var (
		sagaStatus saga.Status
		reason     = "partnerska banka je nedostupna"
	)
	row, err := s.SagaStore.Get(ctx, e.TransactionID)
	switch {
	case err != nil:
		reason = err.Error()
	case row == nil:
		reason = "saga nije pronađena"
	default:
		sagaStatus = row.Status
		reason = saneError(row.LastError, reason)
	}

	switch decideRetryAction(sagaStatus, e.DeadlineAt, now) {
	case retrySucceed:
		if mErr := s.Store.MarkInterbankRetrySucceeded(ctx, e.ID); mErr != nil {
			s.Log.Warn("interbank retry: mark succeeded failed",
				"transaction_id", e.TransactionID, "err", mErr.Error())
			return false
		}
		s.Log.Info("interbank retry: succeeded",
			"transaction_id", e.TransactionID, "attempts", e.AttemptCount)
		return true
	case retryExpire:
		// Past the 30s window (or the saga gave up). Release the local
		// reservation (saga step prepare_local's compensation) so the
		// user's funds are freed, then fail + notify. We release directly
		// rather than through the orchestrator: a saga parked on
		// prepare_partner that exhausts its own attempt budget transitions
		// to StatusFailed WITHOUT running compensations, so the reservation
		// would otherwise leak. RollbackPayment is idempotent.
		s.releaseLocalReservation(ctx, e.TransactionID)
		s.failInterbankRetry(ctx, e, reason)
		return true
	default: // retryReschedule
		next := now.Add(interbankRetryInterval)
		if rErr := s.Store.RescheduleInterbankRetry(ctx, e.ID, next, reason); rErr != nil {
			s.Log.Warn("interbank retry: reschedule failed",
				"transaction_id", e.TransactionID, "err", rErr.Error())
		}
		return false
	}
}

// failInterbankRetry marks the entry failed and notifies the client the
// cross-bank payment did not go through. Notification is best-effort.
func (s *Service) failInterbankRetry(ctx context.Context, e *domain.InterbankRetryEntry, reason string) {
	if mErr := s.Store.MarkInterbankRetryFailed(ctx, e.ID, reason); mErr != nil {
		s.Log.Warn("interbank retry: mark failed failed",
			"transaction_id", e.TransactionID, "err", mErr.Error())
	}
	s.Log.Warn("interbank retry: aborted after deadline",
		"transaction_id", e.TransactionID, "reason", reason)
	s.notifyInterbankFailure(ctx, e, reason)
}

// releaseLocalReservation rolls back the local prepare_local leg of a
// parked cross-bank payment saga so the user's reserved funds are freed
// when the retry queue gives up. Reads the saga payload for the sender
// routing number; best-effort + idempotent (RollbackPayment 404s safely
// when there was no reservation, e.g. the saga never got past validate).
func (s *Service) releaseLocalReservation(ctx context.Context, txID string) {
	if s.InterbankPayer == nil || s.SagaStore == nil {
		return
	}
	row, err := s.SagaStore.Get(ctx, txID)
	if err != nil {
		s.log().WarnContext(ctx, "interbank retry: saga load for rollback failed",
			"err", err, "transaction_id", txID)
		return
	}
	if row == nil {
		s.log().WarnContext(ctx, "interbank retry: saga not found for rollback",
			"transaction_id", txID)
		return
	}
	var payload crossBankPaymentPayload
	if err := json.Unmarshal(row.State, &payload); err != nil {
		s.Log.Warn("interbank retry: decode saga state for rollback failed",
			"transaction_id", txID, "err", err.Error())
		return
	}
	if rErr := s.InterbankPayer.RollbackPayment(ctx,
		payload.SenderRoutingNumber, txID, "cross-bank payment retry deadline reached"); rErr != nil {
		s.Log.Warn("interbank retry: local rollback failed",
			"transaction_id", txID, "err", rErr.Error())
	}
}

// notifyInterbankFailure tells the originator their cross-bank payment
// failed (in-app; the trading service has no email resolver wired for
// this path, same posture as the recurring-order skip notice). Never
// returns an error.
func (s *Service) notifyInterbankFailure(ctx context.Context, e *domain.InterbankRetryEntry, reason string) {
	if s.Notifier == nil {
		return
	}
	title := "Inostrana uplata nije uspela"
	body := fmt.Sprintf("Plaćanje banci %s nije uspelo: %s.", e.PartnerBankCode, reason)
	if err := s.Notifier.InApp(ctx, e.UserID, e.UserKind, "cross_bank_payment", title, body); err != nil {
		s.Log.Warn("interbank retry: in-app notify failed",
			"transaction_id", e.TransactionID, "err", err.Error())
	}
}

// ListInterbankRetries returns the caller's own retry-queue entries,
// newest first — surfaced on the FE for transparency.
func (s *Service) ListInterbankRetries(ctx context.Context) ([]*domain.InterbankRetryEntry, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return s.Store.ListInterbankRetriesByUser(ctx, p.UserID)
}

// saneError returns msg when non-empty, otherwise fallback.
func saneError(msg, fallback string) string {
	if msg == "" {
		return fallback
	}
	return msg
}
