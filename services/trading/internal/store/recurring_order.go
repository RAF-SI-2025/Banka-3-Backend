package store

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

// amount_rsd / quantity are nullable (one is set per mode), so they're
// scanned into pointers and normalised back to the domain's zero values.
const recurringOrderCols = `id, user_id, user_kind, security_id, direction, mode,
    amount_rsd::text, quantity, account_id, cadence, next_run, active, created_at, updated_at`

// InsertRecurringOrder creates one recurring-order row and returns it.
func (s *Store) InsertRecurringOrder(ctx context.Context, r *domain.RecurringOrder) (*domain.RecurringOrder, error) {
	const q = `
        insert into "trading".recurring_orders
            (user_id, user_kind, security_id, direction, mode, amount_rsd, quantity, account_id, cadence, next_run)
        values ($1, $2, $3, $4, $5, $6::numeric, $7, $8, $9, $10)
        returning ` + recurringOrderCols
	var (
		amount *string
		qty    *int32
	)
	if r.AmountRSD != "" {
		amount = &r.AmountRSD
	}
	if r.Quantity > 0 {
		q := r.Quantity
		qty = &q
	}
	row := s.DB.QueryRow(ctx, q,
		r.UserID, string(r.UserKind), r.SecurityID, string(r.Direction), string(r.Mode),
		amount, qty, r.AccountID, r.Cadence, r.NextRun)
	out, err := scanRecurringOrder(row)
	if err != nil {
		return nil, apperr.Internal("insert recurring order", err)
	}
	return out, nil
}

// ListRecurringOrdersByUser returns every recurring order (active +
// paused) for one user, newest first.
func (s *Store) ListRecurringOrdersByUser(ctx context.Context, userID string) ([]*domain.RecurringOrder, error) {
	q := `select ` + recurringOrderCols + ` from "trading".recurring_orders
	      where user_id = $1 order by created_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, userID)
	if err != nil {
		return nil, apperr.Internal("list recurring orders", err)
	}
	defer rows.Close()
	return scanRecurringOrders(rows)
}

// ListDueRecurringOrders returns every active recurring order whose
// next_run is at or before `now` — the cron's working set. Stays on the
// primary (worker read), oldest-due first.
func (s *Store) ListDueRecurringOrders(ctx context.Context, now time.Time) ([]*domain.RecurringOrder, error) {
	q := `select ` + recurringOrderCols + ` from "trading".recurring_orders
	      where active = true and next_run <= $1 order by next_run asc`
	rows, err := s.DB.Query(ctx, q, now)
	if err != nil {
		return nil, apperr.Internal("list due recurring orders", err)
	}
	defer rows.Close()
	return scanRecurringOrders(rows)
}

// GetRecurringOrder returns one recurring order by id. NotFound on miss.
func (s *Store) GetRecurringOrder(ctx context.Context, id string) (*domain.RecurringOrder, error) {
	q := `select ` + recurringOrderCols + ` from "trading".recurring_orders where id = $1`
	out, err := scanRecurringOrder(s.DB.QueryRow(postgres.WithRead(ctx), q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("trajni nalog nije pronađen")
		}
		return nil, apperr.Internal("get recurring order", err)
	}
	return out, nil
}

// SetRecurringOrderActive flips the active flag (pause / resume) and
// bumps updated_at.
func (s *Store) SetRecurringOrderActive(ctx context.Context, id string, active bool) error {
	const q = `update "trading".recurring_orders
	           set active = $2, updated_at = now() where id = $1`
	if _, err := s.DB.Exec(ctx, q, id, active); err != nil {
		return apperr.Internal("set recurring order active", err)
	}
	return nil
}

// UpdateRecurringOrderNextRun advances next_run after the cron fires a
// cycle, bumping updated_at.
func (s *Store) UpdateRecurringOrderNextRun(ctx context.Context, id string, next time.Time) error {
	const q = `update "trading".recurring_orders
	           set next_run = $2, updated_at = now() where id = $1`
	if _, err := s.DB.Exec(ctx, q, id, next); err != nil {
		return apperr.Internal("update recurring order next_run", err)
	}
	return nil
}

// DeleteRecurringOrder removes a recurring order permanently (cancel).
func (s *Store) DeleteRecurringOrder(ctx context.Context, id string) error {
	const q = `delete from "trading".recurring_orders where id = $1`
	if _, err := s.DB.Exec(ctx, q, id); err != nil {
		return apperr.Internal("delete recurring order", err)
	}
	return nil
}

func scanRecurringOrders(rows pgx.Rows) ([]*domain.RecurringOrder, error) {
	var out []*domain.RecurringOrder
	for rows.Next() {
		r, err := scanRecurringOrder(rows)
		if err != nil {
			return nil, apperr.Internal("scan recurring order", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanRecurringOrder(row pgx.Row) (*domain.RecurringOrder, error) {
	var (
		r         domain.RecurringOrder
		kind      string
		direction string
		mode      string
		amount    *string
		qty       *int32
	)
	if err := row.Scan(
		&r.ID, &r.UserID, &kind, &r.SecurityID, &direction, &mode,
		&amount, &qty, &r.AccountID, &r.Cadence, &r.NextRun, &r.Active,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	r.UserKind = domain.UserKind(kind)
	r.Direction = domain.Direction(direction)
	r.Mode = domain.RecurringMode(mode)
	if amount != nil {
		r.AmountRSD = *amount
	}
	if qty != nil {
		r.Quantity = *qty
	}
	return &r, nil
}
