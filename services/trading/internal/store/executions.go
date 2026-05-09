package store

import (
	"context"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const executionCols = `
    id, order_id, quantity,
    price_per_unit::text, total_amount::text, commission_amt::text,
    coalesce(bank_op_id::text, ''), executed_at`

// InsertExecution writes one fill row inside the caller's transaction.
func (s *Store) InsertExecution(ctx context.Context, tx pgx.Tx, e *domain.OrderExecution) (*domain.OrderExecution, error) {
	const q = `
        insert into "trading".order_executions
            (order_id, quantity, price_per_unit, total_amount, commission_amt, bank_op_id)
        values ($1, $2, $3::numeric, $4::numeric, $5::numeric, nullif($6, '')::uuid)
        returning ` + executionCols
	row := tx.QueryRow(ctx, q,
		e.OrderID, e.Quantity, e.PricePerUnit, e.TotalAmount, e.CommissionAmt, e.BankOpID,
	)
	out, err := scanExecution(row)
	if err != nil {
		return nil, apperr.Internal("insert execution", err)
	}
	return out, nil
}

// AdvanceOrderProgress decrements remaining_quantity by `delta` inside
// the caller's tx; sets is_done=true when remaining hits 0; bumps the
// last_modification timestamp.
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
          and cancelled = false
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

// ListExecutions returns all fills for an order in chronological order.
func (s *Store) ListExecutions(ctx context.Context, orderID string) ([]*domain.OrderExecution, error) {
	q := `select ` + executionCols + ` from "trading".order_executions where order_id = $1 order by executed_at`
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
		&e.BankOpID, &e.ExecutedAt,
	); err != nil {
		return nil, err
	}
	return &e, nil
}

// LatestExecutionAt returns the timestamp of the most recent execution
// on an order, or zero-time when none exist yet. Used by the worker to
// pace partial-fill cadence.
func (s *Store) LatestExecutionAt(ctx context.Context, orderID string) (string, error) {
	const q = `select coalesce(max(executed_at)::text, '') from "trading".order_executions where order_id = $1`
	var t string
	if err := s.Pool.QueryRow(ctx, q, orderID).Scan(&t); err != nil {
		return "", apperr.Internal("latest execution", err)
	}
	return strings.TrimSpace(t), nil
}
