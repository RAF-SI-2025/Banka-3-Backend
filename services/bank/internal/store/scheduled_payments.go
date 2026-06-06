package store

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

const scheduledPaymentColumns = `id, client_id, from_account_id, to_account_number, amount, currency,
    recipient_name, coalesce(payment_code, ''), coalesce(purpose, ''), coalesce(model, ''),
    coalesce(reference_number, ''), scheduled_date, status, coalesce(failure_reason, ''),
    created_at, executed_at`

func scanScheduledPayment(row interface{ Scan(...any) error }) (*domain.ScheduledPayment, error) {
	var (
		sp       domain.ScheduledPayment
		currency string
		status   string
	)
	if err := row.Scan(
		&sp.ID, &sp.ClientID, &sp.FromAccountID, &sp.ToAccountNumber, &sp.Amount, &currency,
		&sp.RecipientName, &sp.PaymentCode, &sp.Purpose, &sp.Model,
		&sp.ReferenceNumber, &sp.ScheduledDate, &status, &sp.FailureReason,
		&sp.CreatedAt, &sp.ExecutedAt,
	); err != nil {
		return nil, err
	}
	sp.Currency = domain.Currency(currency)
	sp.Status = domain.ScheduledPaymentStatus(status)
	return &sp, nil
}

// InsertScheduledPayment persists a new 'scheduled' row and returns it.
func (s *Store) InsertScheduledPayment(ctx context.Context, sp *domain.ScheduledPayment) (*domain.ScheduledPayment, error) {
	const q = `
        insert into "bank".scheduled_payments
            (client_id, from_account_id, to_account_number, amount, currency,
             recipient_name, payment_code, purpose, model, reference_number, scheduled_date)
        values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
        returning ` + scheduledPaymentColumns
	out, err := scanScheduledPayment(s.DB.QueryRow(ctx, q,
		sp.ClientID, sp.FromAccountID, sp.ToAccountNumber, sp.Amount, string(sp.Currency),
		sp.RecipientName, sp.PaymentCode, sp.Purpose, sp.Model, sp.ReferenceNumber, sp.ScheduledDate,
	))
	if err != nil {
		return nil, apperr.Internal("insert scheduled payment", err)
	}
	return out, nil
}

// ListScheduledPaymentsByClient returns the client's scheduled payments,
// newest scheduled_date first.
func (s *Store) ListScheduledPaymentsByClient(ctx context.Context, clientID string) ([]*domain.ScheduledPayment, error) {
	const q = `select ` + scheduledPaymentColumns + `
        from "bank".scheduled_payments where client_id = $1 order by scheduled_date desc, created_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, clientID)
	if err != nil {
		return nil, apperr.Internal("list scheduled payments", err)
	}
	defer rows.Close()
	var out []*domain.ScheduledPayment
	for rows.Next() {
		sp, err := scanScheduledPayment(rows)
		if err != nil {
			return nil, apperr.Internal("scan scheduled payment", err)
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// GetScheduledPayment loads a single row by id.
func (s *Store) GetScheduledPayment(ctx context.Context, id string) (*domain.ScheduledPayment, error) {
	const q = `select ` + scheduledPaymentColumns + ` from "bank".scheduled_payments where id = $1`
	sp, err := scanScheduledPayment(s.DB.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("zakazano plaćanje ne postoji")
		}
		return nil, apperr.Internal("get scheduled payment", err)
	}
	return sp, nil
}

// CancelScheduledPayment flips an owner's still-'scheduled' row to
// 'cancelled'. Owner-scoped and status-guarded in one statement so a
// concurrent execution can't be cancelled mid-flight.
func (s *Store) CancelScheduledPayment(ctx context.Context, id, clientID string) (*domain.ScheduledPayment, error) {
	const q = `
        update "bank".scheduled_payments
           set status = 'cancelled'
         where id = $1 and client_id = $2 and status = 'scheduled'
        returning ` + scheduledPaymentColumns
	sp, err := scanScheduledPayment(s.DB.QueryRow(ctx, q, id, clientID))
	if err != nil {
		if noRows(err) {
			// Either the row doesn't exist / isn't the caller's, or it's
			// no longer 'scheduled'. Disambiguate for a useful message.
			existing, gerr := s.GetScheduledPayment(ctx, id)
			if gerr != nil {
				return nil, gerr
			}
			if existing.ClientID != clientID {
				return nil, apperr.NotFound("zakazano plaćanje ne postoji")
			}
			return nil, apperr.FailedPrecondition("zakazano plaćanje se više ne može otkazati")
		}
		return nil, apperr.Internal("cancel scheduled payment", err)
	}
	return sp, nil
}

// ListDueScheduledPayments returns every 'scheduled' row whose
// scheduled_date is at or before now, oldest first. Read against the
// primary so a just-scheduled row isn't missed by replica lag.
func (s *Store) ListDueScheduledPayments(ctx context.Context, now time.Time) ([]*domain.ScheduledPayment, error) {
	const q = `select ` + scheduledPaymentColumns + `
        from "bank".scheduled_payments
       where status = 'scheduled' and scheduled_date <= $1
       order by scheduled_date asc`
	rows, err := s.DB.Query(ctx, q, now)
	if err != nil {
		return nil, apperr.Internal("list due scheduled payments", err)
	}
	defer rows.Close()
	var out []*domain.ScheduledPayment
	for rows.Next() {
		sp, err := scanScheduledPayment(rows)
		if err != nil {
			return nil, apperr.Internal("scan scheduled payment", err)
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// MarkScheduledPaymentCompleted flips a 'scheduled' row to 'completed'
// and stamps executed_at. Status-guarded so a double-sweep is a no-op.
func (s *Store) MarkScheduledPaymentCompleted(ctx context.Context, id string, at time.Time) error {
	const q = `
        update "bank".scheduled_payments
           set status = 'completed', executed_at = $2, failure_reason = null
         where id = $1 and status = 'scheduled'`
	if _, err := s.DB.Exec(ctx, q, id, at); err != nil {
		return apperr.Internal("mark scheduled payment completed", err)
	}
	return nil
}

// MarkScheduledPaymentFailed flips a 'scheduled' row to 'failed' with a
// reason and stamps executed_at. Status-guarded so a double-sweep is a
// no-op.
func (s *Store) MarkScheduledPaymentFailed(ctx context.Context, id, reason string, at time.Time) error {
	const q = `
        update "bank".scheduled_payments
           set status = 'failed', executed_at = $2, failure_reason = $3
         where id = $1 and status = 'scheduled'`
	if _, err := s.DB.Exec(ctx, q, id, at, reason); err != nil {
		return apperr.Internal("mark scheduled payment failed", err)
	}
	return nil
}
