package store

import (
	"context"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

// orderBaseColsUnqualified is the base column list with no table alias,
// for use in RETURNING clauses (where Postgres forbids a table alias).
const orderBaseColsUnqualified = `
    id, user_id, user_kind, security_id, order_type, direction,
    quantity, contract_size::text, price_per_unit::text,
    coalesce(limit_price::text, ''), coalesce(stop_price::text, ''),
    all_or_none, margin, is_actuary, account_id,
    status, coalesce(approved_by::text, ''),
    approval_required, approved_at,
    is_done, cancelled, triggered, after_hours,
    remaining_quantity, last_modification, created_at,
    actor_kind, coalesce(on_behalf_of_fund_id::text, '')`

// orderBaseColsQualified mirrors the above but qualifies every column
// with the `o` alias, for the read paths that LEFT JOIN the execution
// aggregate subquery.
const orderBaseColsQualified = `
    o.id, o.user_id, o.user_kind, o.security_id, o.order_type, o.direction,
    o.quantity, o.contract_size::text, o.price_per_unit::text,
    coalesce(o.limit_price::text, ''), coalesce(o.stop_price::text, ''),
    o.all_or_none, o.margin, o.is_actuary, o.account_id,
    o.status, coalesce(o.approved_by::text, ''),
    o.approval_required, o.approved_at,
    o.is_done, o.cancelled, o.triggered, o.after_hours,
    o.remaining_quantity, o.last_modification, o.created_at,
    o.actor_kind, coalesce(o.on_behalf_of_fund_id::text, '')`

// orderCols projects the base columns plus three empty aggregate
// placeholders so the RETURNING-based mutating queries (create / approve
// / decline / cancel) share scanOrder's column shape. Those paths don't
// need the execution aggregates — they're a read-surface concern.
const orderCols = orderBaseColsUnqualified + `,
    ''::text, ''::text, null::timestamptz`

// orderReadCols projects the base columns plus the per-order execution
// aggregates (todoSpec S30/S31), computed by a LEFT JOIN against a
// pre-aggregated subquery over settled order_executions. Used by the
// read paths (GetOrder / ListOrders) so the history list+detail render
// realized price / paid commission / execution date without an N+1
// fetch. avg_execution_price is quantity-weighted.
const orderReadCols = orderBaseColsQualified + `,
    coalesce(x.avg_execution_price::text, ''),
    coalesce(x.total_commission::text, ''),
    x.last_execution_at`

// orderExecAggJoin is the aggregate subquery joined into the read paths.
// Only settled fills count, matching ListExecutions / LatestExecutionAt.
const orderExecAggJoin = `
    left join (
        select order_id,
               sum(price_per_unit * quantity) / nullif(sum(quantity), 0) as avg_execution_price,
               sum(commission_amt) as total_commission,
               max(executed_at) as last_execution_at
        from "trading".order_executions
        where status = 'settled'
        group by order_id
    ) x on x.order_id = o.id`

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
            approved_by, approved_at,
            actor_kind, on_behalf_of_fund_id
        ) values (
            $1, $2, $3, $4, $5,
            $6, $7::numeric, $8::numeric,
            nullif($9, '')::numeric, nullif($10, '')::numeric,
            $11, $12, $13, $14,
            $15, $16, $17,
            $18, now(),
            nullif($19, '')::uuid,
            case when $20 then now() else null end,
            $21, nullif($22, '')::uuid
        ) returning ` + orderCols
	autoApproved := o.Status == domain.OrderStatusApproved && o.ApprovedBy != ""
	actor := string(o.ActorKind)
	if actor == "" {
		actor = string(o.UserKind)
	}
	row := s.DB.QueryRow(
		ctx, q,
		o.UserID, string(o.UserKind), o.SecurityID, string(o.OrderType), string(o.Direction),
		o.Quantity, o.ContractSize, o.PricePerUnit, o.LimitPrice, o.StopPrice,
		o.AllOrNone, o.Margin, o.IsActuary, o.AccountID,
		string(o.Status), o.ApprovalRequired, o.AfterHours,
		o.Quantity, // remaining_quantity = quantity at create time
		o.ApprovedBy, autoApproved,
		actor, o.OnBehalfOfFundID,
	)
	out, err := scanOrder(row)
	if err != nil {
		return nil, apperr.Internal("create order", err)
	}
	return out, nil
}

// GetOrder returns one order or NotFound.
func (s *Store) GetOrder(ctx context.Context, id string) (*domain.Order, error) {
	q := `select ` + orderReadCols + ` from "trading".orders o` + orderExecAggJoin + ` where o.id = $1`
	out, err := scanOrder(s.DB.QueryRow(ctx, q, id))
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
	// OrderType: "" → no filter; "market"/"limit"/"stop"/"stop_limit"
	// (todoSpec S34). Validated case-insensitively against the known set.
	OrderType string
	// From/To: inclusive bounds on orders.created_at (todoSpec S33).
	// Either may be nil.
	From *time.Time
	To   *time.Time
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
	// todoSpec S34 — order-type filter.
	if ot := strings.ToLower(strings.TrimSpace(f.OrderType)); ot != "" && ot != "all" {
		switch ot {
		case "market", "limit", "stop", "stop_limit":
			add("order_type = ?", ot)
		default:
			return nil, 0, apperr.Validation("nepoznat tip naloga filter: " + f.OrderType)
		}
	}
	// todoSpec S33 — inclusive creation-date range, mirroring the bank
	// ListTransactions from/to convention.
	if f.From != nil {
		add("created_at >= ?", *f.From)
	}
	if f.To != nil {
		add("created_at <= ?", *f.To)
	}

	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}

	var total int64
	if err := s.DB.QueryRow(postgres.WithRead(ctx), `select count(*) from "trading".orders`+where, args...).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("count orders", err)
	}

	// Read path joins the execution-aggregate subquery (todoSpec
	// S30/S31). The WHERE conditions reference unqualified columns that
	// live only on `orders`, so they stay unambiguous under the alias.
	q := `select ` + orderReadCols + ` from "trading".orders o` + orderExecAggJoin + where +
		` order by o.created_at desc limit ` + intArg(len(args)+1) + ` offset ` + intArg(len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)

	rows, err := s.DB.Query(postgres.WithRead(ctx), q, args...)
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
	out, err := scanOrder(s.DB.QueryRow(ctx, q, orderID, approverID))
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
	out, err := scanOrder(s.DB.QueryRow(ctx, q, orderID, approverID))
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
	out, err := scanOrder(s.DB.QueryRow(ctx, q, orderID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.FailedPrecondition("nalog se ne može otkazati")
		}
		return nil, apperr.Internal("cancel order", err)
	}
	return out, nil
}

// PartialCancelOrder reduces the order's target + remaining quantity
// by qty (spec p.57 "otkazivanje celog ili dela"). The WHERE clause's
// remaining_quantity > $2 invariant guarantees we never cross over
// into a full cancel here — the caller routes qty >= remaining to the
// regular CancelOrder path instead. Last_modification bumped per the
// usual audit-trail rules.
func (s *Store) PartialCancelOrder(ctx context.Context, orderID string, qty int32) (*domain.Order, error) {
	const q = `
        update "trading".orders
        set quantity = quantity - $2,
            remaining_quantity = remaining_quantity - $2,
            last_modification = now()
        where id = $1
          and cancelled = false
          and is_done = false
          and remaining_quantity > $2
        returning ` + orderCols
	out, err := scanOrder(s.DB.QueryRow(ctx, q, orderID, qty))
	if err != nil {
		if noRows(err) {
			return nil, apperr.FailedPrecondition("nalog se ne može delimično otkazati")
		}
		return nil, apperr.Internal("partial cancel order", err)
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
	rows, err := s.DB.Query(ctx, q, limit)
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
		actor      string
		approvedAt *time.Time
		lastExecAt *time.Time
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
		&actor, &o.OnBehalfOfFundID,
		&o.AvgExecutionPrice, &o.TotalCommission, &lastExecAt,
	); err != nil {
		return nil, err
	}
	o.UserKind = domain.UserKind(kind)
	o.OrderType = domain.OrderType(typ)
	o.Direction = domain.Direction(dir)
	o.Status = domain.OrderStatus(status)
	o.ActorKind = domain.UserKind(actor)
	o.ApprovedAt = approvedAt
	o.LastExecutionAt = lastExecAt
	return &o, nil
}
