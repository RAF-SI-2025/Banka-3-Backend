package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/schedule"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SchedulePaymentInput is the validated payload for a future-dated
// one-time intra-bank payment (todoSpec C2 "Zakazivanje plaćanja"). It
// mirrors CreatePaymentInput so the due-sweep can replay the same
// money-move at execution time.
type SchedulePaymentInput struct {
	FromAccountID   string
	ToAccountNumber string
	Amount          string
	RecipientName   string
	PaymentCode     string
	ReferenceNumber string
	Model           string
	Purpose         string
	ScheduledDate   time.Time
}

// SchedulePayment validates and persists a future-dated payment in
// status 'scheduled'. The date must be strictly in the future (spec:
// "Sistem proverava da li je datum u budućnosti"). The actual money
// move happens later in RunDueScheduledPayments. Verification is
// enforced at the gateway (the route is in DefaultRules()), same as a
// normal payment.
func (s *Service) SchedulePayment(ctx context.Context, in SchedulePaymentInput) (*domain.ScheduledPayment, error) {
	if err := s.requirePermission(ctx, permissions.PaymentWrite); err != nil {
		return nil, err
	}
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}

	if err := schedule.ValidateFuture(in.ScheduledDate, s.now()); err != nil {
		return nil, apperr.Validation(err.Error())
	}

	// Resolve + ownership-check the endpoints up front so the client
	// gets an immediate error for a bad account/amount rather than a
	// silent failure on the scheduled date.
	from, to, amt, err := s.resolvePaymentEndpoints(ctx, in.FromAccountID, in.ToAccountNumber, in.Amount, p)
	if err != nil {
		return nil, err
	}
	if from.OwnerClientID == to.OwnerClientID {
		return nil, apperr.Validation("plaćanje je između različitih klijenata; za prebacivanje u okviru istog klijenta koristite Prenos")
	}

	code := strings.TrimSpace(in.PaymentCode)
	if code == "" {
		code = "289" // spec p.21 default
	}

	sp := &domain.ScheduledPayment{
		ClientID:        from.OwnerClientID,
		FromAccountID:   from.ID,
		ToAccountNumber: to.Number,
		Amount:          money.FormatAmount(amt),
		Currency:        from.Currency,
		RecipientName:   strings.TrimSpace(in.RecipientName),
		PaymentCode:     code,
		Purpose:         in.Purpose,
		Model:           in.Model,
		ReferenceNumber: in.ReferenceNumber,
		ScheduledDate:   in.ScheduledDate,
	}
	out, err := s.Store.InsertScheduledPayment(ctx, sp)
	if err != nil {
		s.log().ErrorContext(ctx, "schedule payment failed",
			"err", err, "client_id", sp.ClientID, "from_account", from.Number,
			"to_account", to.Number, "amount", sp.Amount, "currency", sp.Currency,
			"scheduled_date", sp.ScheduledDate)
		return nil, err
	}
	s.log().InfoContext(ctx, "payment scheduled",
		"scheduled_payment_id", out.ID, "client_id", out.ClientID,
		"from_account", from.Number, "to_account", out.ToAccountNumber,
		"amount", out.Amount, "currency", out.Currency, "scheduled_date", out.ScheduledDate)
	return out, nil
}

// ListScheduledPayments returns the caller's own scheduled payments.
func (s *Service) ListScheduledPayments(ctx context.Context) ([]*domain.ScheduledPayment, error) {
	if err := s.requirePermission(ctx, permissions.PaymentWrite); err != nil {
		return nil, err
	}
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return s.Store.ListScheduledPaymentsByClient(ctx, p.UserID)
}

// CancelScheduledPayment cancels a still-'scheduled' payment owned by
// the caller. Already-executed / cancelled rows return
// FailedPrecondition (spec: "otkazivanje zakazanih plaćanja pre
// njihovog izvršenja").
func (s *Service) CancelScheduledPayment(ctx context.Context, id string) (*domain.ScheduledPayment, error) {
	if err := s.requirePermission(ctx, permissions.PaymentWrite); err != nil {
		return nil, err
	}
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, apperr.Validation("id is required")
	}
	out, err := s.Store.CancelScheduledPayment(ctx, id, p.UserID)
	if err != nil {
		s.log().WarnContext(ctx, "cancel scheduled payment failed",
			"err", err, "scheduled_payment_id", id, "client_id", p.UserID)
		return nil, err
	}
	s.log().InfoContext(ctx, "scheduled payment cancelled",
		"scheduled_payment_id", out.ID, "client_id", p.UserID,
		"amount", out.Amount, "currency", out.Currency)
	return out, nil
}

// ScheduledPaymentRunResult tallies one due-sweep pass.
type ScheduledPaymentRunResult struct {
	Processed int
	Succeeded int
	Failed    int
}

// RunDueScheduledPayments attempts execution of every scheduled payment
// whose date has arrived. Admin-only (the scheduler presents an admin
// principal). For each due row it replays the intra-bank money-move via
// the shared executeMoneyMove engine, attributing the move to the row's
// client. On success the row is marked 'completed' and the client is
// notified; on insufficient funds it is marked 'failed' and the client
// is notified. Any other error leaves the row 'scheduled' for retry on
// the next sweep.
func (s *Service) RunDueScheduledPayments(ctx context.Context) (*ScheduledPaymentRunResult, error) {
	if err := s.requirePermission(ctx, permissions.Admin); err != nil {
		return nil, err
	}
	now := s.now()
	due, err := s.Store.ListDueScheduledPayments(ctx, now)
	if err != nil {
		s.log().ErrorContext(ctx, "scheduled payment sweep: list due payments failed", "err", err)
		return nil, err
	}

	res := &ScheduledPaymentRunResult{}
	for _, sp := range due {
		res.Processed++
		execErr := s.executeScheduledPayment(ctx, sp)
		at := s.now()
		switch {
		case execErr == nil:
			if err := s.Store.MarkScheduledPaymentCompleted(ctx, sp.ID, at); err != nil {
				// Money moved but the row still reads 'scheduled' — the
				// next sweep would re-execute it; flag loudly.
				s.log().ErrorContext(ctx, "scheduled payment executed but mark-completed failed",
					"err", err, "scheduled_payment_id", sp.ID, "client_id", sp.ClientID,
					"amount", sp.Amount, "currency", sp.Currency)
				continue
			}
			res.Succeeded++
			s.log().InfoContext(ctx, "scheduled payment executed",
				"scheduled_payment_id", sp.ID, "client_id", sp.ClientID,
				"to_account", sp.ToAccountNumber, "amount", sp.Amount, "currency", sp.Currency)
			s.notifyScheduledPaymentSucceeded(ctx, sp)
		case isInsufficientFunds(execErr):
			reason := "nedovoljno sredstava na računu"
			if err := s.Store.MarkScheduledPaymentFailed(ctx, sp.ID, reason, at); err != nil {
				s.log().ErrorContext(ctx, "scheduled payment mark-failed failed",
					"err", err, "scheduled_payment_id", sp.ID, "client_id", sp.ClientID)
				continue
			}
			res.Failed++
			s.log().WarnContext(ctx, "scheduled payment failed: insufficient funds",
				"scheduled_payment_id", sp.ID, "client_id", sp.ClientID,
				"to_account", sp.ToAccountNumber, "amount", sp.Amount, "currency", sp.Currency)
			s.notifyScheduledPaymentFailed(ctx, sp, reason)
		default:
			// Transient/other error — leave 'scheduled' for the next
			// sweep, don't notify (avoids spamming on a flaky run).
			s.log().WarnContext(ctx, "scheduled payment execution deferred",
				"err", execErr, "scheduled_payment_id", sp.ID, "client_id", sp.ClientID,
				"to_account", sp.ToAccountNumber, "amount", sp.Amount, "currency", sp.Currency)
		}
	}
	return res, nil
}

// executeScheduledPayment runs the row's intra-bank money-move through
// the same executeMoneyMove engine CreatePayment uses, attributing the
// move to the row's client (not the admin sweep principal).
func (s *Service) executeScheduledPayment(ctx context.Context, sp *domain.ScheduledPayment) error {
	from, err := s.Store.GetAccountByID(ctx, sp.FromAccountID)
	if err != nil {
		return err
	}
	to, err := s.Store.GetAccountByNumber(ctx, sp.ToAccountNumber)
	if err != nil {
		return err
	}
	amt, err := parsePositive(sp.Amount)
	if err != nil {
		return err
	}

	// The money-move attributes the initiator from this principal so the
	// ledger leg's initiator_client_id is the scheduling client, and the
	// FX-commission branch treats it as a client (not an actuary).
	initiator := auth.Principal{UserID: sp.ClientID, UserKind: auth.KindClient}

	return s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		_, err := s.executeMoneyMove(ctx, tx, from, to, amt, domain.TxKindPayment, uuid.NewString(), initiator, paymentMeta{
			RecipientName:   sp.RecipientName,
			PaymentCode:     sp.PaymentCode,
			ReferenceNumber: sp.ReferenceNumber,
			Purpose:         sp.Purpose,
		}, 0)
		return err
	})
}

// isInsufficientFunds reports whether err is the store's
// insufficient-funds sentinel (used to route a due row to 'failed'
// rather than retry).
func isInsufficientFunds(err error) bool {
	return errors.Is(err, store.ErrInsufficientFunds)
}

func (s *Service) notifyScheduledPaymentSucceeded(ctx context.Context, sp *domain.ScheduledPayment) {
	body := "Poštovani,\n\nZakazano plaćanje na račun " + sp.ToAccountNumber +
		" u iznosu " + sp.Amount + " " + string(sp.Currency) + " je uspešno realizovano.\n\nBanka 3"
	s.notify(ctx, sp.ClientID, "payment", "Zakazano plaćanje je realizovano", body)
}

func (s *Service) notifyScheduledPaymentFailed(ctx context.Context, sp *domain.ScheduledPayment, reason string) {
	body := "Poštovani,\n\nZakazano plaćanje na račun " + sp.ToAccountNumber +
		" u iznosu " + sp.Amount + " " + string(sp.Currency) +
		" nije moglo da se realizuje (" + reason + ").\n\nBanka 3"
	s.notify(ctx, sp.ClientID, "payment", "Zakazano plaćanje nije realizovano", body)
}
