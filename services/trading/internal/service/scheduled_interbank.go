// Scheduled / periodic inter-bank payments (celina 5 — todoSpec
// "Scheduled/periodic inter-bank payments").
//
// A client schedules a cross-bank cash payment to run once on a future
// date or to repeat on a cadence (DAILY/WEEKLY/MONTHLY). Spec example:
// "Svakog prvog u mesecu poslati 400 EUR na dati račun."
//
// On each NextRun the sweep (RunDueInterbankPayments, driven by the
// scheduler's daily `trading-scheduled-interbank` job) drives the
// EXISTING SubmitCrossBankPayment path under a principal scoped to the
// row's owner, then advances NextRun via schedule.AfterRun (ONCE →
// deactivate; recurring → next future slot). Each run reuses the
// cross-bank 2PC saga + retry queue, so a partner outage at run time is
// handled exactly like a user-initiated payment.

package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/schedule"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// CreateScheduledInterbankPaymentInput is the create surface.
type CreateScheduledInterbankPaymentInput struct {
	SourceAccountID   string
	DestBankCode      string
	DestAccountNumber string
	Currency          domain.Currency
	Amount            string
	Purpose           string
	Cadence           schedule.Cadence
	// StartDate (RFC3339) anchors the first NextRun. Required for ONCE
	// (validated to be in the future); for recurring cadences it's
	// optional — empty means the first run is one cadence interval ahead.
	StartDate string
}

// CreateScheduledInterbankPayment registers a scheduled cross-bank
// payment for the caller. Validates the destination shape (same checks
// as the immediate SubmitCrossBankPayment path), the cadence, and the
// start date (future for ONCE via schedule.ValidateFuture). The row is
// Active=true with NextRun set to the chosen start. Principal-scoped:
// only clients with payment.write (or admin) may schedule.
func (s *Service) CreateScheduledInterbankPayment(ctx context.Context, in CreateScheduledInterbankPaymentInput) (*domain.ScheduledInterbankPayment, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.HasAny(p.Permissions, permissions.PaymentWrite, permissions.Admin) {
		return nil, apperr.PermissionDenied("nedovoljne permisije")
	}
	if in.SourceAccountID == "" {
		return nil, apperr.Validation("source_account_id je obavezan")
	}
	if in.DestBankCode == "" {
		return nil, apperr.Validation("destinacijska banka je obavezna")
	}
	if len(in.DestAccountNumber) != 18 {
		return nil, apperr.Validation("destinacijski račun mora imati 18 cifara")
	}
	if !accountNumberChecksumOK(in.DestAccountNumber) {
		return nil, apperr.Validation("checksum destinacijskog računa nije validan")
	}
	if !strings.HasPrefix(in.DestAccountNumber, in.DestBankCode) {
		return nil, apperr.Validation("destinacijski račun ne pripada navedenoj banci")
	}
	if !in.Currency.Supported() {
		return nil, apperr.Validation("nepodržana valuta")
	}
	amt, perr := money.Parse(in.Amount)
	if perr != nil || !money.IsPositive(amt) {
		return nil, apperr.Validation("iznos mora biti pozitivan")
	}
	if !in.Cadence.Valid() {
		return nil, apperr.Validation("učestalost mora biti ONCE, DAILY, WEEKLY ili MONTHLY")
	}

	now := s.now()
	var nextRun time.Time
	switch {
	case in.StartDate != "":
		start, terr := time.Parse(time.RFC3339, in.StartDate)
		if terr != nil {
			return nil, apperr.Validation("neispravan datum početka")
		}
		if verr := schedule.ValidateFuture(start, now); verr != nil {
			return nil, apperr.Validation("datum početka mora biti u budućnosti")
		}
		nextRun = start
	case in.Cadence == schedule.Once:
		// A one-off without a start date has no defined run time.
		return nil, apperr.Validation("datum izvršenja je obavezan za jednokratnu uplatu")
	default:
		// Recurring without an explicit start → first run one interval out.
		nextRun = schedule.Advance(now, in.Cadence)
	}

	return s.Store.InsertScheduledInterbankPayment(ctx, &domain.ScheduledInterbankPayment{
		UserID:            p.UserID,
		UserKind:          domain.UserKind(p.UserKind),
		SourceAccountID:   in.SourceAccountID,
		DestBankCode:      in.DestBankCode,
		DestAccountNumber: in.DestAccountNumber,
		Currency:          in.Currency,
		Amount:            money.FormatAmount(amt),
		Purpose:           in.Purpose,
		Cadence:           string(in.Cadence),
		NextRun:           nextRun,
	})
}

// ListScheduledInterbankPayments returns the caller's own scheduled
// payments (active + paused), newest first.
func (s *Service) ListScheduledInterbankPayments(ctx context.Context) ([]*domain.ScheduledInterbankPayment, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return s.Store.ListScheduledInterbankPaymentsByUser(ctx, p.UserID)
}

// PauseScheduledInterbankPayment flips Active=false so the sweep skips it.
func (s *Service) PauseScheduledInterbankPayment(ctx context.Context, id string) (*domain.ScheduledInterbankPayment, error) {
	return s.setScheduledInterbankActive(ctx, id, false)
}

// ResumeScheduledInterbankPayment flips Active=true so the sweep picks it
// back up.
func (s *Service) ResumeScheduledInterbankPayment(ctx context.Context, id string) (*domain.ScheduledInterbankPayment, error) {
	return s.setScheduledInterbankActive(ctx, id, true)
}

func (s *Service) setScheduledInterbankActive(ctx context.Context, id string, active bool) (*domain.ScheduledInterbankPayment, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.Store.GetScheduledInterbankPayment(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.UserID != p.UserID && !permissions.Has(p.Permissions, permissions.Admin) {
		return nil, apperr.PermissionDenied("nedovoljne permisije")
	}
	if err := s.Store.SetScheduledInterbankPaymentActive(ctx, id, active); err != nil {
		return nil, err
	}
	row.Active = active
	return row, nil
}

// CancelScheduledInterbankPayment deletes a scheduled payment permanently.
func (s *Service) CancelScheduledInterbankPayment(ctx context.Context, id string) error {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return err
	}
	row, err := s.Store.GetScheduledInterbankPayment(ctx, id)
	if err != nil {
		return err
	}
	if row.UserID != p.UserID && !permissions.Has(p.Permissions, permissions.Admin) {
		return apperr.PermissionDenied("nedovoljne permisije")
	}
	return s.Store.DeleteScheduledInterbankPayment(ctx, id)
}

// RunDueInterbankPayments is the sweep entrypoint. For each due + active
// row it submits one cross-bank payment via the existing
// SubmitCrossBankPayment path (under the row owner's principal), records
// the outcome, then advances NextRun via schedule.AfterRun (ONCE →
// deactivate). Returns the number of payments successfully submitted
// (skips/errors don't count). Never fails the whole sweep on one bad row.
func (s *Service) RunDueInterbankPayments(ctx context.Context) (int, error) {
	now := s.now()
	rows, err := s.Store.ListDueScheduledInterbankPayments(ctx, now)
	if err != nil {
		return 0, err
	}
	submitted := 0
	for _, r := range rows {
		status, lastErr := s.runOneScheduledInterbank(ctx, r)
		if lastErr == "" {
			submitted++
		}
		cad := schedule.Cadence(r.Cadence)
		next, deactivate := schedule.AfterRun(r.NextRun, cad, now)
		if aerr := s.Store.AdvanceScheduledInterbankPayment(ctx, r.ID, next, deactivate, status, lastErr, now); aerr != nil {
			s.Log.Warn("scheduled interbank: advance failed",
				"scheduled_interbank_id", r.ID, "err", aerr.Error())
		}
	}
	return submitted, nil
}

// runOneScheduledInterbank submits one due row's cross-bank payment.
// Returns the run's status (the saga status string, or "skipped") and a
// last-error string ("" on a clean submit). Never returns a Go error;
// the caller advances NextRun regardless (a recurring schedule keeps
// firing each cycle, just like the DCA recurring orders).
func (s *Service) runOneScheduledInterbank(ctx context.Context, r *domain.ScheduledInterbankPayment) (statusOut, lastErr string) {
	owner, err := s.recurringOwnerPrincipal(ctx, &domain.RecurringOrder{UserID: r.UserID, UserKind: r.UserKind})
	if err != nil {
		s.Log.Warn("scheduled interbank: owner principal resolution failed; skipping",
			"scheduled_interbank_id", r.ID, "err", err.Error())
		return "skipped", "vlasnik naloga nije mogao biti razrešen"
	}
	ownerCtx := auth.WithPrincipal(ctx, owner)

	// Deterministic idempotency key per row+run so a re-fired sweep tick
	// (or a saga already in flight) never double-charges: keyed by the
	// scheduled row id + the scheduled run time.
	idem := fmt.Sprintf("scheduled:%s:%d", r.ID, r.NextRun.Unix())
	res, err := s.SubmitCrossBankPayment(ownerCtx, SubmitCrossBankPaymentInput{
		IdempotencyKey:      idem,
		SourceAccountID:     r.SourceAccountID,
		RemoteBankCode:      r.DestBankCode,
		RemoteAccountNumber: r.DestAccountNumber,
		Currency:            r.Currency,
		Amount:              r.Amount,
		Purpose:             r.Purpose,
	})
	if err != nil {
		s.Log.Warn("scheduled interbank: submit failed; skipping cycle",
			"scheduled_interbank_id", r.ID, "err", err.Error())
		s.notifyScheduledInterbankSkip(ctx, r, "uplata nije mogla biti izvršena ovog ciklusa")
		return "failed", err.Error()
	}
	// A 'running' status means the partner was unavailable and the retry
	// queue is now driving it — that's a successful submit from the
	// sweep's POV (the retry worker owns the outcome + client notice).
	return res.Status, ""
}

// notifyScheduledInterbankSkip tells the owner a scheduled cross-bank
// payment couldn't be submitted this cycle. In-app, best-effort.
func (s *Service) notifyScheduledInterbankSkip(ctx context.Context, r *domain.ScheduledInterbankPayment, reason string) {
	if s.Notifier == nil {
		return
	}
	title := "Zakazana inostrana uplata preskočena"
	body := fmt.Sprintf("Zakazana uplata banci %s nije izvršena: %s.", r.DestBankCode, reason)
	if err := s.Notifier.InApp(ctx, r.UserID, r.UserKind, "cross_bank_payment", title, body); err != nil {
		s.Log.Warn("scheduled interbank: in-app notify failed",
			"scheduled_interbank_id", r.ID, "err", err.Error())
	}
}
