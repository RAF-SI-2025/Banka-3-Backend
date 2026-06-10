package store

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const interbankRetryCols = `id, transaction_id, partner_bank_code, operation,
    user_id, user_kind, attempt_count, next_retry_at, deadline_at, status,
    last_error, created_at, updated_at`

// EnqueueInterbankRetry inserts (or re-arms) one retry-queue entry for a
// parked cross-bank payment. Idempotent on transaction_id: an existing
// entry is re-armed to pending with a fresh next_retry_at; the deadline
// stays anchored to the original created_at (so the 30s window does not
// reset on every re-enqueue). Returns the resulting row.
func (s *Store) EnqueueInterbankRetry(ctx context.Context, e *domain.InterbankRetryEntry) (*domain.InterbankRetryEntry, error) {
	const q = `
        insert into "trading".interbank_retry_queue
            (transaction_id, partner_bank_code, operation, user_id, user_kind,
             attempt_count, next_retry_at, deadline_at, status)
        values ($1, $2, $3, $4, $5, 0, $6, $7, 'pending')
        on conflict (transaction_id) do update set
            next_retry_at = excluded.next_retry_at,
            status        = case when "trading".interbank_retry_queue.status = 'succeeded'
                                 then "trading".interbank_retry_queue.status
                                 else 'pending' end,
            updated_at    = now()
        returning ` + interbankRetryCols
	row := s.DB.QueryRow(ctx, q,
		e.TransactionID, e.PartnerBankCode, e.Operation, e.UserID,
		string(e.UserKind), e.NextRetryAt, e.DeadlineAt)
	out, err := scanInterbankRetry(row)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "enqueue interbank retry failed", "err", err)
		return nil, apperr.Internal("enqueue interbank retry", err)
	}
	return out, nil
}

// ListDueInterbankRetries returns every pending entry whose next_retry_at
// is at or before `now` — the worker's working set, oldest-due first.
func (s *Store) ListDueInterbankRetries(ctx context.Context, now time.Time) ([]*domain.InterbankRetryEntry, error) {
	q := `select ` + interbankRetryCols + ` from "trading".interbank_retry_queue
	      where status = 'pending' and next_retry_at <= $1 order by next_retry_at asc`
	rows, err := s.DB.Query(ctx, q, now)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list due interbank retries failed", "err", err)
		return nil, apperr.Internal("list due interbank retries", err)
	}
	defer rows.Close()
	out, err := scanInterbankRetries(rows)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list due interbank retries failed", "err", err)
	}
	return out, err
}

// ListInterbankRetriesByUser returns every retry entry (any status) for
// one user, newest first — surfaced on the FE for transparency.
func (s *Store) ListInterbankRetriesByUser(ctx context.Context, userID string) ([]*domain.InterbankRetryEntry, error) {
	q := `select ` + interbankRetryCols + ` from "trading".interbank_retry_queue
	      where user_id = $1 order by created_at desc`
	rows, err := s.DB.Query(ctx, q, userID)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list interbank retries failed", "err", err, "user_id", userID)
		return nil, apperr.Internal("list interbank retries", err)
	}
	defer rows.Close()
	out, err := scanInterbankRetries(rows)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list interbank retries by user failed", "err", err, "user_id", userID)
	}
	return out, err
}

// MarkInterbankRetrySucceeded flips an entry to succeeded.
func (s *Store) MarkInterbankRetrySucceeded(ctx context.Context, id string) error {
	const q = `update "trading".interbank_retry_queue
	           set status = 'succeeded', updated_at = now() where id = $1`
	if _, err := s.DB.Exec(ctx, q, id); err != nil {
		logger.From(ctx).ErrorContext(ctx, "mark interbank retry succeeded failed", "err", err, "id", id)
		return apperr.Internal("mark interbank retry succeeded", err)
	}
	return nil
}

// MarkInterbankRetryFailed flips an entry to failed and records the error.
func (s *Store) MarkInterbankRetryFailed(ctx context.Context, id, lastErr string) error {
	const q = `update "trading".interbank_retry_queue
	           set status = 'failed', last_error = $2, updated_at = now() where id = $1`
	if _, err := s.DB.Exec(ctx, q, id, lastErr); err != nil {
		logger.From(ctx).ErrorContext(ctx, "mark interbank retry failed failed", "err", err, "id", id)
		return apperr.Internal("mark interbank retry failed", err)
	}
	return nil
}

// RescheduleInterbankRetry bumps attempt_count, records the last error,
// and arms next_retry_at for another attempt (next = now + 5s).
func (s *Store) RescheduleInterbankRetry(ctx context.Context, id string, nextRetryAt time.Time, lastErr string) error {
	const q = `update "trading".interbank_retry_queue
	           set attempt_count = attempt_count + 1,
	               next_retry_at = $2,
	               last_error = $3,
	               updated_at = now()
	           where id = $1`
	if _, err := s.DB.Exec(ctx, q, id, nextRetryAt, lastErr); err != nil {
		logger.From(ctx).ErrorContext(ctx, "reschedule interbank retry failed", "err", err, "id", id)
		return apperr.Internal("reschedule interbank retry", err)
	}
	return nil
}

func scanInterbankRetries(rows pgx.Rows) ([]*domain.InterbankRetryEntry, error) {
	var out []*domain.InterbankRetryEntry
	for rows.Next() {
		e, err := scanInterbankRetry(rows)
		if err != nil {
			return nil, apperr.Internal("scan interbank retry", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanInterbankRetry(row pgx.Row) (*domain.InterbankRetryEntry, error) {
	var (
		e      domain.InterbankRetryEntry
		kind   string
		status string
	)
	if err := row.Scan(
		&e.ID, &e.TransactionID, &e.PartnerBankCode, &e.Operation,
		&e.UserID, &kind, &e.AttemptCount, &e.NextRetryAt, &e.DeadlineAt,
		&status, &e.LastError, &e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return nil, err
	}
	e.UserKind = domain.UserKind(kind)
	e.Status = domain.InterbankRetryStatus(status)
	return &e, nil
}
