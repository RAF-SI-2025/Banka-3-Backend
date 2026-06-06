package store

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const scheduledInterbankCols = `id, user_id, user_kind, source_account_id,
    dest_bank_code, dest_account_number, currency, amount::text, purpose,
    cadence, next_run, active, last_status, last_error, last_run_at,
    created_at, updated_at`

// InsertScheduledInterbankPayment creates one scheduled cross-bank
// payment row and returns it.
func (s *Store) InsertScheduledInterbankPayment(ctx context.Context, p *domain.ScheduledInterbankPayment) (*domain.ScheduledInterbankPayment, error) {
	const q = `
        insert into "trading".scheduled_interbank_payments
            (user_id, user_kind, source_account_id, dest_bank_code,
             dest_account_number, currency, amount, purpose, cadence, next_run)
        values ($1, $2, $3, $4, $5, $6, $7::numeric, $8, $9, $10)
        returning ` + scheduledInterbankCols
	row := s.DB.QueryRow(ctx, q,
		p.UserID, string(p.UserKind), p.SourceAccountID, p.DestBankCode,
		p.DestAccountNumber, string(p.Currency), p.Amount, p.Purpose,
		p.Cadence, p.NextRun)
	out, err := scanScheduledInterbank(row)
	if err != nil {
		return nil, apperr.Internal("insert scheduled interbank payment", err)
	}
	return out, nil
}

// ListScheduledInterbankPaymentsByUser returns every scheduled payment
// (active + paused) for one user, newest first.
func (s *Store) ListScheduledInterbankPaymentsByUser(ctx context.Context, userID string) ([]*domain.ScheduledInterbankPayment, error) {
	q := `select ` + scheduledInterbankCols + ` from "trading".scheduled_interbank_payments
	      where user_id = $1 order by created_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, userID)
	if err != nil {
		return nil, apperr.Internal("list scheduled interbank payments", err)
	}
	defer rows.Close()
	return scanScheduledInterbanks(rows)
}

// ListDueScheduledInterbankPayments returns every active row whose
// next_run is at or before `now` — the sweep's working set, oldest-due
// first. Stays on the primary (worker read).
func (s *Store) ListDueScheduledInterbankPayments(ctx context.Context, now time.Time) ([]*domain.ScheduledInterbankPayment, error) {
	q := `select ` + scheduledInterbankCols + ` from "trading".scheduled_interbank_payments
	      where active = true and next_run <= $1 order by next_run asc`
	rows, err := s.DB.Query(ctx, q, now)
	if err != nil {
		return nil, apperr.Internal("list due scheduled interbank payments", err)
	}
	defer rows.Close()
	return scanScheduledInterbanks(rows)
}

// GetScheduledInterbankPayment returns one row by id. NotFound on miss.
func (s *Store) GetScheduledInterbankPayment(ctx context.Context, id string) (*domain.ScheduledInterbankPayment, error) {
	q := `select ` + scheduledInterbankCols + ` from "trading".scheduled_interbank_payments where id = $1`
	out, err := scanScheduledInterbank(s.DB.QueryRow(postgres.WithRead(ctx), q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("zakazana inostrana uplata nije pronađena")
		}
		return nil, apperr.Internal("get scheduled interbank payment", err)
	}
	return out, nil
}

// SetScheduledInterbankPaymentActive flips the active flag (pause /
// resume) and bumps updated_at.
func (s *Store) SetScheduledInterbankPaymentActive(ctx context.Context, id string, active bool) error {
	const q = `update "trading".scheduled_interbank_payments
	           set active = $2, updated_at = now() where id = $1`
	if _, err := s.DB.Exec(ctx, q, id, active); err != nil {
		return apperr.Internal("set scheduled interbank payment active", err)
	}
	return nil
}

// AdvanceScheduledInterbankPayment records a run's outcome: the new
// next_run, whether the row should deactivate (ONCE), and the last
// status/error to surface on the FE. Bumps updated_at + last_run_at.
func (s *Store) AdvanceScheduledInterbankPayment(ctx context.Context, id string, next time.Time, deactivate bool, lastStatus, lastErr string, ranAt time.Time) error {
	const q = `update "trading".scheduled_interbank_payments
	           set next_run = $2,
	               active = case when $3 then false else active end,
	               last_status = $4,
	               last_error = $5,
	               last_run_at = $6,
	               updated_at = now()
	           where id = $1`
	if _, err := s.DB.Exec(ctx, q, id, next, deactivate, lastStatus, lastErr, ranAt); err != nil {
		return apperr.Internal("advance scheduled interbank payment", err)
	}
	return nil
}

// DeleteScheduledInterbankPayment removes a scheduled payment permanently
// (cancel).
func (s *Store) DeleteScheduledInterbankPayment(ctx context.Context, id string) error {
	const q = `delete from "trading".scheduled_interbank_payments where id = $1`
	if _, err := s.DB.Exec(ctx, q, id); err != nil {
		return apperr.Internal("delete scheduled interbank payment", err)
	}
	return nil
}

func scanScheduledInterbanks(rows pgx.Rows) ([]*domain.ScheduledInterbankPayment, error) {
	var out []*domain.ScheduledInterbankPayment
	for rows.Next() {
		p, err := scanScheduledInterbank(rows)
		if err != nil {
			return nil, apperr.Internal("scan scheduled interbank payment", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanScheduledInterbank(row pgx.Row) (*domain.ScheduledInterbankPayment, error) {
	var (
		p        domain.ScheduledInterbankPayment
		kind     string
		currency string
		lastRun  *time.Time
	)
	if err := row.Scan(
		&p.ID, &p.UserID, &kind, &p.SourceAccountID, &p.DestBankCode,
		&p.DestAccountNumber, &currency, &p.Amount, &p.Purpose, &p.Cadence,
		&p.NextRun, &p.Active, &p.LastStatus, &p.LastError, &lastRun,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	p.UserKind = domain.UserKind(kind)
	p.Currency = domain.Currency(currency)
	p.LastRunAt = lastRun
	return &p, nil
}
