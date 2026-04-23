package trading

import (
	"context"
	"errors"
	"strings"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"github.com/RAF-SI-2025/Banka-3-Backend/internal/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// requirePending rejects any order whose current status isn't "pending",
// enforcing the "single-shot" rule on approve/decline (spec p.57: a repeat
// call must return FailedPrecondition).
func requirePending(s OrderStatus) error {
	if s != StatusPending {
		return status.Errorf(codes.FailedPrecondition, "order is %s, not pending", s)
	}
	return nil
}

// requireCancellable accepts pending and approved orders and rejects every
// terminal state (done / declined / already-cancelled). Used by CancelOrder
// so repeat cancels on the same row surface as FailedPrecondition.
func requireCancellable(s OrderStatus) error {
	switch s {
	case StatusPending, StatusApproved:
		return nil
	}
	return status.Errorf(codes.FailedPrecondition, "order is %s; only pending/approved can be cancelled", s)
}

// parseListStatus accepts the supervisor portal's status filter (spec p.57).
// Empty or "all" means no filter; anything else must be a valid OrderStatus.
// Returning an empty string tells the caller to skip the WHERE clause.
func parseListStatus(s string) (OrderStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "all":
		return "", nil
	case "pending":
		return StatusPending, nil
	case "approved":
		return StatusApproved, nil
	case "declined":
		return StatusDeclined, nil
	case "done":
		return StatusDone, nil
	case "cancelled", "canceled":
		return StatusCancelled, nil
	}
	return "", status.Errorf(codes.InvalidArgument, "invalid status filter %q", s)
}

// orderSettlementDate re-resolves the underlying's settlement date for an
// existing order row. Stocks and forex have no expiry; futures carry one on
// the futures row, options carry one directly. Used by approve to enforce
// spec p.57: "orders whose underlying is past settlement allow only decline".
func orderSettlementDate(tx *gorm.DB, o *Order) (*time.Time, error) {
	switch {
	case o.OptionID != nil:
		var opt Option
		if err := tx.Select("settlement_date").First(&opt, *o.OptionID).Error; err != nil {
			return nil, err
		}
		sd := opt.SettlementDate
		return &sd, nil
	case o.ListingID != nil:
		var listing Listing
		if err := tx.Select("future_id").First(&listing, *o.ListingID).Error; err != nil {
			return nil, err
		}
		if listing.FutureID == nil {
			return nil, nil
		}
		var fut Future
		if err := tx.Select("settlement_date").First(&fut, *listing.FutureID).Error; err != nil {
			return nil, err
		}
		sd := fut.SettlementDate
		return &sd, nil
	}
	return nil, nil
}

// assetLabelForOrder produces the human-readable ticker shown in the portal
// table. Listings resolve through their stock/future row; options and forex
// carry a ticker directly.
func assetLabelForOrder(tx *gorm.DB, o *Order) (string, error) {
	switch {
	case o.ListingID != nil:
		var row struct {
			Ticker string
		}
		err := tx.Raw(`
			SELECT COALESCE(s.ticker, f.ticker, '') AS ticker
			FROM listings l
			LEFT JOIN stocks  s ON s.id = l.stock_id
			LEFT JOIN futures f ON f.id = l.future_id
			WHERE l.id = ?
		`, *o.ListingID).Scan(&row).Error
		if err != nil {
			return "", err
		}
		return row.Ticker, nil
	case o.OptionID != nil:
		var opt Option
		if err := tx.Select("ticker").First(&opt, *o.OptionID).Error; err != nil {
			return "", err
		}
		return opt.Ticker, nil
	case o.ForexPairID != nil:
		var fx ForexPair
		if err := tx.Select("ticker").First(&fx, *o.ForexPairID).Error; err != nil {
			return "", err
		}
		return fx.Ticker, nil
	}
	return "", nil
}

// placerInfo is the denormalized identity/label for an order's placer. Name
// is "first last" regardless of whether the placer is an employee or a
// client, so the portal renders a single "agent" column (spec p.57).
type placerInfo struct {
	Name       string
	EmployeeID int64
	ClientID   int64
}

// loadPlacerInfo joins the order_placers row through to either employees or
// clients to pull a display name. Exactly one of employee_id / client_id is
// set on the placer row (schema CHECK), so the left joins collapse to
// whichever side is populated.
func loadPlacerInfo(tx *gorm.DB, placerID int64) (placerInfo, error) {
	var row struct {
		EmployeeID *int64 `gorm:"column:employee_id"`
		ClientID   *int64 `gorm:"column:client_id"`
		EmpFirst   string `gorm:"column:emp_first"`
		EmpLast    string `gorm:"column:emp_last"`
		CliFirst   string `gorm:"column:cli_first"`
		CliLast    string `gorm:"column:cli_last"`
	}
	err := tx.Raw(`
		SELECT p.employee_id, p.client_id,
		       e.first_name AS emp_first, e.last_name AS emp_last,
		       c.first_name AS cli_first, c.last_name AS cli_last
		FROM order_placers p
		LEFT JOIN employees e ON e.id = p.employee_id
		LEFT JOIN clients   c ON c.id = p.client_id
		WHERE p.id = ?
	`, placerID).Scan(&row).Error
	if err != nil {
		return placerInfo{}, err
	}
	info := placerInfo{}
	if row.EmployeeID != nil {
		info.EmployeeID = *row.EmployeeID
		info.Name = strings.TrimSpace(row.EmpFirst + " " + row.EmpLast)
	}
	if row.ClientID != nil {
		info.ClientID = *row.ClientID
		info.Name = strings.TrimSpace(row.CliFirst + " " + row.CliLast)
	}
	return info, nil
}

// buildOrderDetail assembles the portal row for a single order. Called from
// ListOrders (in a loop) and from the approve/decline/cancel responses.
func (s *Server) buildOrderDetail(tx *gorm.DB, o *Order, now time.Time) (*tradingpb.OrderDetail, error) {
	label, err := assetLabelForOrder(tx, o)
	if err != nil {
		return nil, err
	}
	placer, err := loadPlacerInfo(tx, o.PlacerID)
	if err != nil {
		return nil, err
	}
	sd, err := orderSettlementDate(tx, o)
	if err != nil {
		return nil, err
	}
	detail := &tradingpb.OrderDetail{
		Id:                o.ID,
		Status:            string(o.Status),
		OrderType:         string(o.OrderType),
		Direction:         string(o.Direction),
		Quantity:          o.Quantity,
		ContractSize:      o.ContractSize,
		PricePerUnit:      o.PricePerUnit,
		RemainingPortions: o.RemainingPortions,
		AssetLabel:        label,
		PlacerName:        placer.Name,
		PlacerEmployeeId:  placer.EmployeeID,
		PlacerClientId:    placer.ClientID,
		PastSettlement:    isPastSettlement(sd, now),
		CreatedAtUnix:     o.CreatedAt.Unix(),
		Margin:            o.Margin,
		AllOrNone:         o.AllOrNone,
		Commission:        o.Commission,
	}
	if o.ApprovedBy != nil {
		detail.ApprovedBy = *o.ApprovedBy
	}
	return detail, nil
}

// ListOrders returns the supervisor's orders portal view (spec pp.57–58).
// Supervisor-only — the gateway gates with `secured("supervisor")` and we
// re-check here. agent_id filters on the placer's employee_id; passing a
// positive id naturally excludes client-placed orders since the join needs
// a non-null employee_id.
func (s *Server) ListOrders(_ context.Context, req *tradingpb.ListOrdersRequest) (*tradingpb.ListOrdersResponse, error) {
	if !callerIsSupervisor(s.db, req.CallerEmail) {
		return nil, status.Error(codes.PermissionDenied, "supervisor permission required")
	}

	filter, err := parseListStatus(req.Status)
	if err != nil {
		return nil, err
	}

	q := s.db.Model(&Order{}).Joins("JOIN order_placers p ON p.id = orders.placer_id")
	if filter != "" {
		q = q.Where("orders.status = ?", string(filter))
	}
	if req.AgentId > 0 {
		q = q.Where("p.employee_id = ?", req.AgentId)
	}
	q = q.Order("orders.created_at DESC")

	var orders []Order
	if err := q.Find(&orders).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	now := time.Now()
	out := make([]*tradingpb.OrderDetail, 0, len(orders))
	for i := range orders {
		d, err := s.buildOrderDetail(s.db, &orders[i], now)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		out = append(out, d)
	}
	return &tradingpb.ListOrdersResponse{Orders: out}, nil
}

// ApproveOrder transitions a pending order to approved. Single-shot: any
// non-pending status (including a prior approve) returns FailedPrecondition.
// If the underlying is past settlement the approve is refused — the
// supervisor can only decline such orders (spec p.57). approved_by is set to
// the supervisor's employee id; the agent's used_limit is consumed at this
// point so subsequent orders see the updated headroom.
func (s *Server) ApproveOrder(_ context.Context, req *tradingpb.ApproveOrderRequest) (*tradingpb.ApproveOrderResponse, error) {
	if req.OrderId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "order_id required")
	}
	supID, err := supervisorEmployeeID(s.db, req.CallerEmail)
	if err != nil {
		return nil, err
	}

	var detail *tradingpb.OrderDetail
	err = s.db.Transaction(func(tx *gorm.DB) error {
		order, err := lockOrder(tx, req.OrderId)
		if err != nil {
			return err
		}
		if err := requirePending(order.Status); err != nil {
			return err
		}
		sd, err := orderSettlementDate(tx, order)
		if err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		if isPastSettlement(sd, time.Now()) {
			return status.Error(codes.FailedPrecondition, "underlying is past settlement; decline only")
		}

		if err := tx.Model(order).Updates(map[string]any{
			"status":      string(StatusApproved),
			"approved_by": supID,
		}).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		order.Status = StatusApproved
		order.ApprovedBy = &supID

		if err := consumeAgentLimitOnApproval(tx, order, s.bank); err != nil {
			return err
		}

		d, err := s.buildOrderDetail(tx, order, time.Now())
		if err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		detail = d
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &tradingpb.ApproveOrderResponse{Order: detail}, nil
}

// DeclineOrder transitions a pending order to declined. Single-shot: any
// non-pending status returns FailedPrecondition. Commission isn't refunded
// here — the orders table doesn't carry the debit account_number, so a
// refund has no target; spec pp.57–58 don't mandate one. Left for the
// execution engine (#205) to wire once per-fill accounting lands.
func (s *Server) DeclineOrder(_ context.Context, req *tradingpb.DeclineOrderRequest) (*tradingpb.DeclineOrderResponse, error) {
	if req.OrderId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "order_id required")
	}
	if _, err := supervisorEmployeeID(s.db, req.CallerEmail); err != nil {
		return nil, err
	}

	var detail *tradingpb.OrderDetail
	err := s.db.Transaction(func(tx *gorm.DB) error {
		order, err := lockOrder(tx, req.OrderId)
		if err != nil {
			return err
		}
		if err := requirePending(order.Status); err != nil {
			return err
		}

		if err := tx.Model(order).Update("status", string(StatusDeclined)).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		order.Status = StatusDeclined

		d, err := s.buildOrderDetail(tx, order, time.Now())
		if err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		detail = d
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &tradingpb.DeclineOrderResponse{Order: detail}, nil
}

// CancelOrder withdraws the remaining, unfilled portion of an order. The
// caller must either be the placer (owner) or carry supervisor /
// trading_cancel permissions (spec p.58). Pending and approved orders can be
// cancelled; done, declined, and already-cancelled ones return
// FailedPrecondition.
func (s *Server) CancelOrder(ctx context.Context, req *tradingpb.CancelOrderRequest) (*tradingpb.CancelOrderResponse, error) {
	if req.OrderId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "order_id required")
	}
	caller, err := s.bank.ResolveCaller(ctx)
	if err != nil {
		return nil, err
	}

	var detail *tradingpb.OrderDetail
	err = s.db.Transaction(func(tx *gorm.DB) error {
		order, err := lockOrder(tx, req.OrderId)
		if err != nil {
			return err
		}
		if err := requireCancellable(order.Status); err != nil {
			return err
		}
		if err := authorizeCancel(tx, order, caller); err != nil {
			return err
		}

		if err := tx.Model(order).Updates(map[string]any{
			"status":             string(StatusCancelled),
			"remaining_portions": 0,
		}).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		order.Status = StatusCancelled
		order.RemainingPortions = 0

		d, err := s.buildOrderDetail(tx, order, time.Now())
		if err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		detail = d
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &tradingpb.CancelOrderResponse{Order: detail}, nil
}

// authorizeCancel enforces the spec's "owner or supervisor" rule, with an
// extra carveout for employees holding the seeded trading_cancel permission.
// Owners are matched on the placer row so clients can only cancel rows they
// actually placed; employees who own the order are also allowed through the
// ownership path (no perm needed) to mirror how CreateOrder's placer semantics
// work for agent-placed orders.
func authorizeCancel(tx *gorm.DB, order *Order, caller *bank.CallerIdentity) error {
	var placer OrderPlacer
	if err := tx.First(&placer, order.PlacerID).Error; err != nil {
		return status.Errorf(codes.Internal, "%v", err)
	}

	if caller.IsClient && placer.ClientID != nil && *placer.ClientID == caller.ClientID {
		return nil
	}
	if caller.IsEmployee {
		if callerIsSupervisor(tx, caller.Email) {
			return nil
		}
		if callerHasTradingCancel(tx, caller.Email) {
			return nil
		}
		if placer.EmployeeID != nil && callerEmployeeID(tx, caller.Email) == *placer.EmployeeID {
			return nil
		}
	}
	return status.Error(codes.PermissionDenied, "only the placer or a supervisor may cancel this order")
}

// callerHasTradingCancel checks for the `trading_cancel` permission (admin
// bypasses). Same shape as callerHasMarginPermission — trading stays in the
// bank process so it queries employee_permissions directly.
func callerHasTradingCancel(db *gorm.DB, email string) bool {
	if strings.TrimSpace(email) == "" {
		return false
	}
	var count int64
	err := db.Table("employees").
		Joins("JOIN employee_permissions ep ON ep.employee_id = employees.id").
		Joins("JOIN permissions p ON p.id = ep.permission_id").
		Where("employees.email = ? AND p.name IN (?)", email, []string{"admin", "trading_cancel"}).
		Count(&count).Error
	if err != nil {
		return false
	}
	return count > 0
}

// callerEmployeeID returns the employee's primary key for the given email,
// or 0 if the lookup fails (matches authorizeCancel's "fall through" shape —
// a miss means the caller doesn't own the placer row).
func callerEmployeeID(db *gorm.DB, email string) int64 {
	if strings.TrimSpace(email) == "" {
		return 0
	}
	var id int64
	err := db.Table("employees").Select("id").Where("email = ?", email).Take(&id).Error
	if err != nil {
		return 0
	}
	return id
}

// supervisorEmployeeID verifies the caller carries supervisor/admin and
// returns their employee id in one shot. Used by approve to populate
// approved_by and by decline to gate the call. PermissionDenied is the only
// caller-visible error; NotFound on the email bubbles up as the same
// permission error to avoid leaking whether the email exists.
func supervisorEmployeeID(db *gorm.DB, email string) (int64, error) {
	if strings.TrimSpace(email) == "" {
		return 0, status.Error(codes.PermissionDenied, "supervisor permission required")
	}
	var row struct {
		ID int64
	}
	err := db.Table("employees").
		Select("employees.id").
		Joins("JOIN employee_permissions ep ON ep.employee_id = employees.id").
		Joins("JOIN permissions p ON p.id = ep.permission_id").
		Where("employees.email = ? AND p.name IN (?)", email, []string{"admin", "supervisor"}).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, status.Error(codes.PermissionDenied, "supervisor permission required")
		}
		return 0, status.Errorf(codes.Internal, "%v", err)
	}
	return row.ID, nil
}

// consumeAgentLimitOnApproval mirrors the self-approve path in CreateOrder: if
// the placer is an agent (not supervisor/client), their used_limit grows by
// the order's approximate RSD notional. Done here rather than at placement
// because pending orders deliberately skip the increment (spec p.39 — limit
// consumed only once the order is live).
func consumeAgentLimitOnApproval(tx *gorm.DB, order *Order, bankSrv *bank.Server) error {
	var placer OrderPlacer
	if err := tx.First(&placer, order.PlacerID).Error; err != nil {
		return status.Errorf(codes.Internal, "%v", err)
	}
	if placer.EmployeeID == nil {
		return nil
	}

	var emp struct {
		Email string
	}
	if err := tx.Table("employees").Select("email").Where("id = ?", *placer.EmployeeID).Take(&emp).Error; err != nil {
		return status.Errorf(codes.Internal, "%v", err)
	}
	_, role, _, err := resolveEmployeeRole(tx, emp.Email)
	if err != nil {
		return err
	}
	if role != roleAgent {
		return nil
	}

	currency, err := orderInstrumentCurrency(tx, order)
	if err != nil {
		return err
	}
	// Market orders carry price_per_unit=0 on the row (execution re-reads the
	// quote at fill time) so the notional must come from the current market
	// price, not the order row. Non-market orders store the user-provided
	// limit/stop price and can use it directly.
	pricePerUnit := order.PricePerUnit
	if order.OrderType == OrderMarket && pricePerUnit == 0 {
		pricePerUnit, err = orderMarketPrice(tx, order)
		if err != nil {
			return err
		}
	}
	approxNative := order.ContractSize * pricePerUnit * order.Quantity
	var approxRSD int64
	if currency == "RSD" {
		approxRSD = approxNative
	} else {
		rate, err := bankSrv.GetExchangeRateToRSD(currency)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to load exchange rate for %s: %v", currency, err)
		}
		approxRSD = int64(float64(approxNative) * rate)
	}

	return tx.Table("employees").
		Where("id = ?", *placer.EmployeeID).
		Update("used_limit", gorm.Expr("used_limit + ?", approxRSD)).Error
}

// orderMarketPrice re-reads the current quote for a market order whose row
// stores price_per_unit=0. Listings use the listing price, options use the
// premium, forex uses the usual ExchangeRate*100 convention (matching
// resolveInstrument at placement).
func orderMarketPrice(tx *gorm.DB, o *Order) (int64, error) {
	switch {
	case o.ListingID != nil:
		var l Listing
		if err := tx.Select("price").First(&l, *o.ListingID).Error; err != nil {
			return 0, status.Errorf(codes.Internal, "%v", err)
		}
		return l.Price, nil
	case o.OptionID != nil:
		var opt Option
		if err := tx.Select("premium").First(&opt, *o.OptionID).Error; err != nil {
			return 0, status.Errorf(codes.Internal, "%v", err)
		}
		return opt.Premium, nil
	case o.ForexPairID != nil:
		var fx ForexPair
		if err := tx.Select("exchange_rate").First(&fx, *o.ForexPairID).Error; err != nil {
			return 0, status.Errorf(codes.Internal, "%v", err)
		}
		return int64(fx.ExchangeRate * 100), nil
	}
	return 0, status.Error(codes.Internal, "order has no underlying reference")
}

// orderInstrumentCurrency recovers the currency originally used for commission
// / approval math. Listings inherit exchange currency; options follow the
// underlying's listing; forex uses its quote currency. Kept separate from
// resolveInstrument to avoid re-loading market prices we don't need here.
func orderInstrumentCurrency(tx *gorm.DB, o *Order) (string, error) {
	switch {
	case o.ListingID != nil:
		var row struct {
			Currency string
		}
		err := tx.Raw(`
			SELECT e.currency FROM listings l
			JOIN exchanges e ON e.id = l.exchange_id
			WHERE l.id = ?
		`, *o.ListingID).Scan(&row).Error
		if err != nil {
			return "", err
		}
		return row.Currency, nil
	case o.OptionID != nil:
		var row struct {
			Currency string
		}
		err := tx.Raw(`
			SELECT e.currency FROM options o
			JOIN listings l  ON l.stock_id = o.stock_id
			JOIN exchanges e ON e.id = l.exchange_id
			WHERE o.id = ?
			LIMIT 1
		`, *o.OptionID).Scan(&row).Error
		if err != nil {
			return "", err
		}
		return row.Currency, nil
	case o.ForexPairID != nil:
		var fx ForexPair
		if err := tx.Select("quote_currency").First(&fx, *o.ForexPairID).Error; err != nil {
			return "", err
		}
		return fx.QuoteCurrency, nil
	}
	return "", status.Error(codes.Internal, "order has no underlying reference")
}

// lockOrder loads an order row FOR UPDATE so concurrent approve/decline/cancel
// calls serialize through Postgres row locks. NotFound becomes a gRPC error
// directly; other failures bubble up as Internal through the transaction
// wrapper.
func lockOrder(tx *gorm.DB, id int64) (*Order, error) {
	var o Order
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&o, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "order not found")
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &o, nil
}
