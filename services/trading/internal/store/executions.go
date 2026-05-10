package store

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const executionCols = `
    id, order_id, quantity,
    price_per_unit::text, total_amount::text, commission_amt::text,
    coalesce(bank_op_id::text, ''), status, executed_at`

// InsertPendingExecution writes a status='pending' fill row inside the
// caller's tx. The returned UUID is the deterministic op_id passed to
// the bank's settle RPC: idempotent on retry, and recoverable after a
// worker crash because the row is durable evidence we've committed to
// settling this fill.
func (s *Store) InsertPendingExecution(ctx context.Context, tx pgx.Tx, e *domain.OrderExecution) (*domain.OrderExecution, error) {
	const q = `
        insert into "trading".order_executions
            (order_id, quantity, price_per_unit, total_amount, commission_amt, status)
        values ($1, $2, $3::numeric, $4::numeric, $5::numeric, 'pending')
        returning ` + executionCols
	row := tx.QueryRow(ctx, q,
		e.OrderID, e.Quantity, e.PricePerUnit, e.TotalAmount, e.CommissionAmt,
	)
	out, err := scanExecution(row)
	if err != nil {
		return nil, apperr.Internal("insert pending execution", err)
	}
	return out, nil
}

// MarkExecutionSettled flips a pending row to status='settled' and stamps
// the bank-side op_id (may be empty for forex fills with no settler
// wired in dev). Idempotent: a row already settled is left alone.
func (s *Store) MarkExecutionSettled(ctx context.Context, tx pgx.Tx, execID, bankOpID string) error {
	const q = `
        update "trading".order_executions
        set status     = 'settled',
            bank_op_id = nullif($2, '')::uuid
        where id = $1`
	if _, err := tx.Exec(ctx, q, execID, bankOpID); err != nil {
		return apperr.Internal("mark execution settled", err)
	}
	return nil
}

// GetPendingExecutionForOrder returns the in-flight pending fill (if any)
// for an order. Used by the worker to resume a fill after a crash.
// Returns (nil, nil) when no pending row exists. There is at most one
// pending row per order at a time — the saga commits to one fill before
// starting the next.
func (s *Store) GetPendingExecutionForOrder(ctx context.Context, orderID string) (*domain.OrderExecution, error) {
	q := `select ` + executionCols + ` from "trading".order_executions
	      where order_id = $1 and status = 'pending'
	      order by executed_at asc
	      limit 1`
	out, err := scanExecution(s.Pool.QueryRow(ctx, q, orderID))
	if err != nil {
		if noRows(err) {
			return nil, nil
		}
		return nil, apperr.Internal("get pending execution", err)
	}
	return out, nil
}

// ListOrderIDsWithPendingExecutions returns the set of order IDs that
// have at least one pending fill row. Used by the worker on each tick
// to drive the recovery sweep before the regular active-order sweep.
//
// The list isn't deduplicated against the regular active set — the
// worker's per-order tick handler is a no-op for an order without
// pending rows when called twice.
func (s *Store) ListOrderIDsWithPendingExecutions(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
        select distinct order_id::text
        from "trading".order_executions
        where status = 'pending'
        order by order_id::text
        limit $1`
	rows, err := s.Pool.Query(ctx, q, limit)
	if err != nil {
		return nil, apperr.Internal("list pending exec orders", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, apperr.Internal("scan pending exec order id", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AdvanceOrderProgress decrements remaining_quantity by `delta` inside
// the caller's tx; sets is_done=true when remaining hits 0; bumps the
// last_modification timestamp.
//
// We deliberately don't gate on `cancelled = false` here: by the time
// AdvanceOrderProgress runs, a pending row has already committed the
// service to booking this fill (bank may have settled). Cancellation
// only stops *future* fills — sealed fills are always booked, per spec
// p.50 ("sealed fills stay"). ProcessOrderTick gates fresh fills on
// `o.Cancelled` upstream so cancelled orders never start new pending
// rows.
//
// Returns the post-update remaining_quantity so callers can detect
// completion without a second round-trip.
func (s *Store) AdvanceOrderProgress(ctx context.Context, tx pgx.Tx, orderID string, delta int32) (int32, error) {
	const q = `
        update "trading".orders
        set remaining_quantity = remaining_quantity - $2,
            is_done            = (remaining_quantity - $2) = 0,
            last_modification  = now()
        where id = $1
          and remaining_quantity >= $2
          and status = 'approved'
        returning remaining_quantity`
	var remaining int32
	err := tx.QueryRow(ctx, q, orderID, delta).Scan(&remaining)
	if err != nil {
		if noRows(err) {
			return 0, apperr.FailedPrecondition("nalog nije aktivan ili nedovoljno preostale količine")
		}
		return 0, apperr.Internal("advance order", err)
	}
	return remaining, nil
}

// SetOrderTriggered flips orders.triggered=true inside the caller's tx.
// Idempotent: a no-op if already triggered.
func (s *Store) SetOrderTriggered(ctx context.Context, tx pgx.Tx, orderID string) error {
	const q = `update "trading".orders set triggered = true, last_modification = now() where id = $1`
	if _, err := tx.Exec(ctx, q, orderID); err != nil {
		return apperr.Internal("set triggered", err)
	}
	return nil
}

// ListExecutions returns all settled fills for an order in chronological
// order. Pending rows are filtered out — they're worker-internal state
// and shouldn't surface to the API.
func (s *Store) ListExecutions(ctx context.Context, orderID string) ([]*domain.OrderExecution, error) {
	q := `select ` + executionCols + ` from "trading".order_executions
	      where order_id = $1 and status = 'settled'
	      order by executed_at`
	rows, err := s.Pool.Query(ctx, q, orderID)
	if err != nil {
		return nil, apperr.Internal("list executions", err)
	}
	defer rows.Close()
	var out []*domain.OrderExecution
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, apperr.Internal("scan execution", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanExecution(row pgx.Row) (*domain.OrderExecution, error) {
	var e domain.OrderExecution
	if err := row.Scan(
		&e.ID, &e.OrderID, &e.Quantity,
		&e.PricePerUnit, &e.TotalAmount, &e.CommissionAmt,
		&e.BankOpID, &e.Status, &e.ExecutedAt,
	); err != nil {
		return nil, err
	}
	return &e, nil
}

// LatestExecutionAt returns the timestamp of the most recent settled
// execution on an order, or (zero, false) when none exist yet. Used by
// the worker to pace partial-fill cadence — pending rows are excluded
// so a stuck pending row doesn't reset cadence to "just fired".
func (s *Store) LatestExecutionAt(ctx context.Context, orderID string) (time.Time, bool, error) {
	const q = `
        select max(executed_at)
        from "trading".order_executions
        where order_id = $1 and status = 'settled'`
	var t *time.Time
	if err := s.Pool.QueryRow(ctx, q, orderID).Scan(&t); err != nil {
		return time.Time{}, false, apperr.Internal("latest execution", err)
	}
	if t == nil {
		return time.Time{}, false, nil
	}
	return *t, true, nil
}
