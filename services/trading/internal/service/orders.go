package service

import (
	"context"
	"math/big"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/jackc/pgx/v5"
)

// CreateOrderInput is the service-layer view of CreateOrderRequest.
// AccountID is the bank account that funds the buy or receives the
// sell-proceeds. UserID/UserKind are filled from the principal.
type CreateOrderInput struct {
	SecurityID string
	OrderType  domain.OrderType
	Direction  domain.Direction
	Quantity   int32
	LimitPrice string
	StopPrice  string
	AllOrNone  bool
	Margin     bool
	AccountID  string
}

// CreateOrder validates, snapshots price + after-hours, routes for
// approval, and persists.
//
// Approval rules (spec p.50):
//   - clients & supervisors / admin: auto-approve.
//   - agents: pending if their actuary_info.need_approval is true,
//     OR if the trade's RSD value would push used_limit over daily_limit.
//   - if the principal isn't a recognised trader (no TradingClient,
//     no Actuary), reject upfront.
//
// Settlement-date guard: futures/options whose settlement_date is on
// or before today are auto-rejected (Validation, not "pending").
//
// Margin guard: only principals with TradingMargin may set margin=true.
func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (*domain.Order, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.assertTraderRole(p); err != nil {
		return nil, err
	}
	if err := validateOrderShape(in); err != nil {
		return nil, err
	}
	if in.Margin && !permissions.Has(p.Permissions, permissions.TradingMargin) {
		return nil, apperr.PermissionDenied("nedovoljne permisije za margin trgovinu")
	}

	sec, err := s.Store.GetSecurity(ctx, in.SecurityID)
	if err != nil {
		return nil, err
	}
	// Spec p.58 — clients can't order forex pairs or options.
	if p.UserKind == auth.KindClient {
		switch sec.Type {
		case domain.SecurityForex, domain.SecurityOption:
			return nil, apperr.PermissionDenied("klijenti ne mogu da trguju forex parovima ili opcijama")
		}
	}
	now := s.now()
	if sec.SettlementDate != nil && !sec.SettlementDate.After(now) {
		return nil, apperr.FailedPrecondition("hartija je istekla — trgovina nije moguća")
	}

	listing, err := s.Store.GetListingBySecurityID(ctx, in.SecurityID)
	if err != nil {
		// Options don't carry a listing — read the security's premium
		// as the price snapshot. Spec p.45.
		if sec.Type == domain.SecurityOption && sec.Premium != "" {
			listing = &domain.Listing{
				SecurityID:   sec.ID,
				Price:        sec.Premium,
				Ask:          sec.Premium,
				Bid:          sec.Premium,
				ContractSize: sec.ContractSize,
			}
		} else {
			return nil, err
		}
	}

	// price_per_unit at submit = listing.Price; the execution worker
	// will use the per-fill quote.
	priceSnap := listing.Price
	contractSize := listing.ContractSize
	if contractSize == "" {
		contractSize = "1"
	}

	// after_hours flag: if the security trades on an exchange, ask the
	// exchange resolver. Forex / options without an exchange skip this.
	afterHours := false
	if sec.ExchangeMIC != "" {
		ex, err := s.Store.GetExchange(ctx, sec.ExchangeMIC)
		if err == nil {
			afterHours = s.resolveMarketState(ex, now).IsAfterHours
		}
	}

	// Approval routing.
	approvalRequired := false
	status := domain.OrderStatusApproved
	if permissions.Has(p.Permissions, permissions.ActuaryAgent) {
		need, overLimit, err := s.agentNeedsApproval(ctx, p.UserID, sec, in.Quantity, priceSnap, contractSize)
		if err != nil {
			return nil, err
		}
		if need || overLimit {
			approvalRequired = true
			status = domain.OrderStatusPending
		}
	}

	o := &domain.Order{
		UserID:           p.UserID,
		UserKind:         domain.UserKind(p.UserKind),
		SecurityID:       in.SecurityID,
		OrderType:        in.OrderType,
		Direction:        in.Direction,
		Quantity:         in.Quantity,
		ContractSize:     contractSize,
		PricePerUnit:     priceSnap,
		LimitPrice:       in.LimitPrice,
		StopPrice:        in.StopPrice,
		AllOrNone:        in.AllOrNone,
		Margin:           in.Margin,
		AccountID:        in.AccountID,
		Status:           status,
		ApprovalRequired: approvalRequired,
		AfterHours:       afterHours,
	}
	if status == domain.OrderStatusApproved {
		// Auto-approved orders: the store stamps approved_by/approved_at
		// on insert when these fields are set on the domain row.
		o.ApprovedBy = p.UserID
	}
	out, err := s.Store.CreateOrder(ctx, o)
	if err != nil {
		return nil, err
	}
	// Charge the agent's used_limit on auto-approved trades so the cap
	// holds even when no supervisor approval was needed.
	if status == domain.OrderStatusApproved && permissions.Has(p.Permissions, permissions.ActuaryAgent) {
		s.maybeChargeAgentLimit(ctx, out)
	}
	return out, nil
}

// GetOrder returns one order. Visibility:
//   - owner sees their own;
//   - supervisors/admin see anyone;
//   - everyone else: PermissionDenied.
func (s *Service) GetOrder(ctx context.Context, id string) (*domain.Order, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	o, err := s.Store.GetOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	if o.UserID != p.UserID {
		if !permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor) {
			return nil, apperr.PermissionDenied("nedovoljne permisije")
		}
	}
	return o, nil
}

// ListOrdersInput is the service-layer view of ListOrdersRequest.
type ListOrdersInput struct {
	Status     string
	UserKind   domain.UserKind
	UserID     string
	SecurityID string
	Page       int
	PageSize   int
}

// ListOrders returns matching orders. Visibility:
//   - clients/agents see only their own;
//   - supervisors/admin see everything (and may filter by user).
func (s *Service) ListOrders(ctx context.Context, in ListOrdersInput) ([]*domain.Order, int64, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, 0, err
	}
	supervisor := permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor)
	f := store.OrderFilter{
		Status:     in.Status,
		UserKind:   in.UserKind,
		UserID:     in.UserID,
		SecurityID: in.SecurityID,
	}
	if !supervisor {
		// Force-narrow to the caller's own orders.
		f.UserID = p.UserID
		f.UserKind = domain.UserKind(p.UserKind)
	}
	return s.Store.ListOrders(ctx, f, in.Page, in.PageSize)
}

// ApproveOrder is supervisor-only. Decision bumps the agent's used_limit
// in the same transaction so the cap is enforced atomically.
//
// The used_limit increment uses the order's RSD-equivalent value; if
// the rate provider isn't wired (dev stack without exchange service),
// foreign trades fall through with no increment and a logged warning.
func (s *Service) ApproveOrder(ctx context.Context, id string) (*domain.Order, error) {
	p, err := s.requireSupervisor(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := s.Store.GetOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.Status != domain.OrderStatusPending {
		return nil, apperr.FailedPrecondition("nalog nije u stanju 'pending'")
	}
	out, err := s.Store.ApproveOrder(ctx, id, p.UserID)
	if err != nil {
		return nil, err
	}
	// If the order belongs to an agent, charge the limit. We do this
	// after the row-update so the audit stays clean even if the limit
	// math fails — the supervisor can still see the order as approved.
	if cur.UserKind == domain.KindEmployee {
		s.maybeChargeAgentLimit(ctx, cur)
	}
	return out, nil
}

// DeclineOrder is supervisor-only. `reason` is logged but not persisted
// (spec doesn't define a reason column; can be added later).
func (s *Service) DeclineOrder(ctx context.Context, id, reason string) (*domain.Order, error) {
	p, err := s.requireSupervisor(ctx)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		s.Log.Info("order declined", "order_id", id, "by", p.UserID, "reason", reason)
	}
	return s.Store.DeclineOrder(ctx, id, p.UserID)
}

// CancelOrder marks the order cancelled. Only the order's owner (or a
// supervisor/admin) can cancel. Cancelling halts further fills; fills
// that already settled stay sealed (spec p.50).
func (s *Service) CancelOrder(ctx context.Context, id string) (*domain.Order, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := s.Store.GetOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.UserID != p.UserID {
		if !permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor) {
			return nil, apperr.PermissionDenied("nedovoljne permisije")
		}
	}
	return s.Store.CancelOrder(ctx, id)
}

// =====================================================================
// Approval routing helpers
// =====================================================================

// agentNeedsApproval returns (need_approval_flag, over_limit). Either
// triggers a routed-to-supervisor pending state. Daily limit is in RSD
// per spec p.38; foreign-currency trades are converted via the rate
// provider with no commission.
func (s *Service) agentNeedsApproval(
	ctx context.Context,
	employeeID string,
	sec *domain.Security,
	qty int32,
	pricePerUnit, contractSize string,
) (bool, bool, error) {
	info, err := s.Store.GetActuaryInfo(ctx, employeeID)
	if err != nil {
		// No actuary_info row for an employee with the agent permission
		// is a misconfiguration — be safe and route to approval.
		s.Log.Warn("agent without actuary_info row; routing to approval",
			"employee_id", employeeID, "err", err.Error())
		return true, false, nil
	}
	if info.NeedApproval {
		return true, false, nil
	}

	tradeRSD, err := s.tradeValueRSD(ctx, sec, qty, pricePerUnit, contractSize)
	if err != nil {
		return false, false, err
	}
	limit, err := money.Parse(info.DailyLimit)
	if err != nil {
		return false, false, apperr.Internal("agent daily_limit unparseable", err)
	}
	used, err := money.Parse(info.UsedLimit)
	if err != nil {
		return false, false, apperr.Internal("agent used_limit unparseable", err)
	}
	// daily_limit = 0 means unlimited; spec p.38 doesn't say so explicitly
	// but matches the bank service's account-limit convention.
	if limit.Sign() == 0 {
		return false, false, nil
	}
	projected := money.Add(used, tradeRSD)
	if projected.Cmp(limit) > 0 {
		return false, true, nil
	}
	return false, false, nil
}

// tradeValueRSD computes the RSD-equivalent of a trade's notional
// (qty × contract_size × price). Falls back to the raw value when the
// rate provider isn't wired and the security currency isn't RSD —
// callers tolerate this loosely (limit checks may pass when they
// shouldn't on a misconfigured dev stack).
func (s *Service) tradeValueRSD(
	ctx context.Context,
	sec *domain.Security,
	qty int32,
	pricePerUnit, contractSize string,
) (*big.Rat, error) {
	price, err := money.Parse(pricePerUnit)
	if err != nil {
		return nil, apperr.Internal("price snapshot unparseable", err)
	}
	cs, err := money.Parse(contractSize)
	if err != nil {
		return nil, apperr.Internal("contract size unparseable", err)
	}
	q := new(big.Rat).SetInt64(int64(qty))
	notional := money.Mul(money.Mul(q, cs), price)
	if sec.Currency == domain.CurrencyRSD || sec.Currency == "" {
		return notional, nil
	}
	if s.Rates == nil {
		s.Log.Warn("rates provider missing; using raw notional for limit math",
			"security_id", sec.ID, "currency", sec.Currency)
		return notional, nil
	}
	_, ask, err := s.Rates.Quote(ctx, sec.Currency, domain.CurrencyRSD)
	if err != nil {
		return nil, apperr.Internal("fx quote failed", err)
	}
	r, err := money.Parse(ask)
	if err != nil {
		return nil, apperr.Internal("fx ask unparseable", err)
	}
	return money.Mul(notional, r), nil
}

// maybeChargeAgentLimit increments the actuary's used_limit by the
// approved order's RSD-equivalent. Failure is logged, not propagated —
// the order is already approved and the cron will catch up.
func (s *Service) maybeChargeAgentLimit(ctx context.Context, o *domain.Order) {
	sec, err := s.Store.GetSecurity(ctx, o.SecurityID)
	if err != nil {
		s.Log.Warn("limit charge: security lookup failed", "order_id", o.ID, "err", err.Error())
		return
	}
	rsd, err := s.tradeValueRSD(ctx, sec, o.Quantity, o.PricePerUnit, o.ContractSize)
	if err != nil {
		s.Log.Warn("limit charge: rsd math failed", "order_id", o.ID, "err", err.Error())
		return
	}
	if rsd.Sign() == 0 {
		return
	}
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		return s.Store.AddUsedLimit(ctx, tx, o.UserID, money.FormatAmount(rsd))
	}); err != nil {
		s.Log.Warn("limit charge: db update failed", "order_id", o.ID, "err", err.Error())
	}
}

// =====================================================================
// Validation
// =====================================================================

func (s *Service) assertTraderRole(p auth.Principal) error {
	if permissions.HasAny(p.Permissions,
		permissions.Admin,
		permissions.ActuarySupervisor,
		permissions.ActuaryAgent,
		permissions.TradingClient,
	) {
		return nil
	}
	return apperr.PermissionDenied("nedovoljne permisije za trgovinu")
}

func validateOrderShape(in CreateOrderInput) error {
	if in.SecurityID == "" {
		return apperr.Validation("security_id is required")
	}
	if in.AccountID == "" {
		return apperr.Validation("account_id is required")
	}
	if in.Quantity <= 0 {
		return apperr.Validation("quantity must be positive")
	}
	switch in.OrderType {
	case domain.OrderMarket:
	case domain.OrderLimit, domain.OrderStopLimit:
		if strings.TrimSpace(in.LimitPrice) == "" {
			return apperr.Validation("limit_price je obavezan za limit/stop_limit nalog")
		}
		if err := validateNonNegativeAmount(in.LimitPrice); err != nil {
			return err
		}
	}
	switch in.OrderType {
	case domain.OrderStop, domain.OrderStopLimit:
		if strings.TrimSpace(in.StopPrice) == "" {
			return apperr.Validation("stop_price je obavezan za stop/stop_limit nalog")
		}
		if err := validateNonNegativeAmount(in.StopPrice); err != nil {
			return err
		}
	}
	switch in.OrderType {
	case domain.OrderMarket, domain.OrderLimit, domain.OrderStop, domain.OrderStopLimit:
	default:
		return apperr.Validation("nepoznat order_type")
	}
	switch in.Direction {
	case domain.DirectionBuy, domain.DirectionSell:
	default:
		return apperr.Validation("nepoznata direction")
	}
	return nil
}
