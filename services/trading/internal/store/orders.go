package store

import (
	"context"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const orderCols = `
    id, user_id, user_kind, security_id, order_type, direction,
    quantity, contract_size::text, price_per_unit::text,
    coalesce(limit_price::text, ''), coalesce(stop_price::text, ''),
    all_or_none, margin, is_actuary, account_id,
    status, coalesce(approved_by::text, ''),
    approval_required, approved_at,
    is_done, cancelled, triggered, after_hours,
    remaining_quantity, last_modification, created_at`

// CreateOrder inserts the order row and returns it. When the caller
// auto-approved the order (status='approved' with ApprovedBy set), the
// approver/approved_at columns are stamped on insert so the audit row
// is complete.
func (s *Store) CreateOrder(ctx context.Context, o *domain.Order) (*domain.Order, error) {
	const q = `
        insert into "trading".orders (
            user_id, user_kind, security_id, order_type, direction,
            quantity, contract_size, price_per_unit, limit_price, stop_price,
            all_or_none, margin, is_actuary, account_id,
            status, approval_required, after_hours,
            remaining_quantity, last_modification,
            approved_by, approved_at
        ) values (
            $1, $2, $3, $4, $5,
            $6, $7::numeric, $8::numeric,
            nullif($9, '')::numeric, nullif($10, '')::numeric,
            $11, $12, $13, $14,
            $15, $16, $17,
            $18, now(),
            nullif($19, '')::uuid,
            case when $20 then now() else null end
        ) returning ` + orderCols
	autoApproved := o.Status == domain.OrderStatusApproved && o.ApprovedBy != ""
	row := s.Pool.QueryRow(ctx, q,
		o.UserID, string(o.UserKind), o.SecurityID, string(o.OrderType), string(o.Direction),
		o.Quantity, o.ContractSize, o.PricePerUnit, o.LimitPrice, o.StopPrice,
		o.AllOrNone, o.Margin, o.IsActuary, o.AccountID,
		string(o.Status), o.ApprovalRequired, o.AfterHours,
		o.Quantity, // remaining_quantity = quantity at create time
		o.ApprovedBy, autoApproved,
	)
	out, err := scanOrder(row)
	if err != nil {
		return nil, apperr.Internal("create order", err)
	}
	return out, nil
}

// GetOrder returns one order or NotFound.
func (s *Store) GetOrder(ctx context.Context, id string) (*domain.Order, error) {
	q := `select ` + orderCols + ` from "trading".orders where id = $1`
	out, err := scanOrder(s.Pool.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("nalog ne postoji")
		}
		return nil, apperr.Internal("get order", err)
	}
	return out, nil
}

// OrderFilter narrows ListOrders. Empty fields are ignored.
type OrderFilter struct {
	// Status: "" / "all" → no filter; "pending"/"approved"/"declined";
	// "active" → status='approved' and not done and not cancelled;
	// "done" → is_done=true.
	Status     string
	UserKind   domain.UserKind
	UserID     string
	SecurityID string
}

// ListOrders returns matching orders with paging.
func (s *Store) ListOrders(ctx context.Context, f OrderFilter, page, pageSize int) ([]*domain.Order, int64, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	if page <= 0 {
		page = 1
	}

	var args []any
	var conds []string
	add := func(cond string, a any) {
		args = append(args, a)
		conds = append(conds, strings.ReplaceAll(cond, "?", intArg(len(args))))
	}
	switch strings.ToLower(strings.TrimSpace(f.Status)) {
	case "", "all":
	case "pending", "approved", "declined":
		add("status = ?", strings.ToLower(f.Status))
	case "active":
		conds = append(conds, "status = 'approved' and is_done = false and cancelled = false")
	case "done":
		conds = append(conds, "is_done = true")
	default:
		return nil, 0, apperr.Validation("nepoznat status filter: " + f.Status)
	}
	if f.UserKind != "" {
		add("user_kind = ?", string(f.UserKind))
	}
	if f.UserID != "" {
		add("user_id = ?", f.UserID)
	}
	if f.SecurityID != "" {
		add("security_id = ?", f.SecurityID)
	}

	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}

	var total int64
	if err := s.Pool.QueryRow(ctx, `select count(*) from "trading".orders`+where, args...).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("count orders", err)
	}

	q := `select ` + orderCols + ` from "trading".orders` + where +
		` order by created_at desc limit ` + intArg(len(args)+1) + ` offset ` + intArg(len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)

	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, apperr.Internal("list orders", err)
	}
	defer rows.Close()
	var out []*domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, 0, apperr.Internal("scan order", err)
		}
		out = append(out, o)
	}
	return out, total, rows.Err()
}

// ApproveOrder marks the order approved and stamps approver/timestamp.
// Only valid when current status='pending' and not cancelled. Returns
// the new row.
func (s *Store) ApproveOrder(ctx context.Context, orderID, approverID string) (*domain.Order, error) {
	const q = `
        update "trading".orders
        set status = 'approved', approved_by = $2, approved_at = now(),
            last_modification = now()
        where id = $1 and status = 'pending' and cancelled = false
        returning ` + orderCols
	out, err := scanOrder(s.Pool.QueryRow(ctx, q, orderID, approverID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.FailedPrecondition("nalog nije u stanju 'pending'")
		}
		return nil, apperr.Internal("approve order", err)
	}
	return out, nil
}

// DeclineOrder marks the order declined.
func (s *Store) DeclineOrder(ctx context.Context, orderID, approverID string) (*domain.Order, error) {
	const q = `
        update "trading".orders
        set status = 'declined', approved_by = $2, approved_at = now(),
            last_modification = now()
        where id = $1 and status = 'pending' and cancelled = false
        returning ` + orderCols
	out, err := scanOrder(s.Pool.QueryRow(ctx, q, orderID, approverID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.FailedPrecondition("nalog nije u stanju 'pending'")
		}
		return nil, apperr.Internal("decline order", err)
	}
	return out, nil
}

// CancelOrder marks the order cancelled. Allowed while the order is
// still active (not done, not already cancelled). Spec p.50: cancelling
// stops further fills but keeps the executions that already happened.
func (s *Store) CancelOrder(ctx context.Context, orderID string) (*domain.Order, error) {
	const q = `
        update "trading".orders
        set cancelled = true, last_modification = now()
        where id = $1 and cancelled = false and is_done = false
        returning ` + orderCols
	out, err := scanOrder(s.Pool.QueryRow(ctx, q, orderID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.FailedPrecondition("nalog se ne može otkazati")
		}
		return nil, apperr.Internal("cancel order", err)
	}
	return out, nil
}

// CancelOrderTx is the tx-bound idempotent variant used by the recovery
// sweep when it abandons a pending row. Unlike CancelOrder it tolerates
// an already-cancelled or done order — the goal is "ensure this order
// is not going to start a fresh fill", not "the caller is the canonical
// cancel actor". No-op on a row that's already cancelled.
func (s *Store) CancelOrderTx(ctx context.Context, tx pgx.Tx, orderID string) error {
	const q = `
        update "trading".orders
        set cancelled = true, last_modification = now()
        where id = $1 and cancelled = false`
	if _, err := tx.Exec(ctx, q, orderID); err != nil {
		return apperr.Internal("cancel order (tx)", err)
	}
	return nil
}

// GetActiveOrdersForExecution returns approved+active orders for the
// execution worker. Used by Phase C; landed now so the order surface
// is complete.
func (s *Store) GetActiveOrdersForExecution(ctx context.Context, limit int) ([]*domain.Order, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `select ` + orderCols + ` from "trading".orders
	      where status = 'approved' and cancelled = false and is_done = false
	      order by last_modification asc
	      limit $1`
	rows, err := s.Pool.Query(ctx, q, limit)
	if err != nil {
		return nil, apperr.Internal("active orders", err)
	}
	defer rows.Close()
	var out []*domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, apperr.Internal("scan active order", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func scanOrder(row pgx.Row) (*domain.Order, error) {
	var (
		o          domain.Order
		kind       string
		typ        string
		dir        string
		status     string
		approvedAt *time.Time
	)
	if err := row.Scan(
		&o.ID, &o.UserID, &kind, &o.SecurityID, &typ, &dir,
		&o.Quantity, &o.ContractSize, &o.PricePerUnit,
		&o.LimitPrice, &o.StopPrice,
		&o.AllOrNone, &o.Margin, &o.IsActuary, &o.AccountID,
		&status, &o.ApprovedBy,
		&o.ApprovalRequired, &approvedAt,
		&o.IsDone, &o.Cancelled, &o.Triggered, &o.AfterHours,
		&o.RemainingQuantity, &o.LastModification, &o.CreatedAt,
	); err != nil {
		return nil, err
	}
	o.UserKind = domain.UserKind(kind)
	o.OrderType = domain.OrderType(typ)
	o.Direction = domain.Direction(dir)
	o.Status = domain.OrderStatus(status)
	o.ApprovedAt = approvedAt
	return &o, nil
}

