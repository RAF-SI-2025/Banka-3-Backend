package service

import (
	"context"
	"math/big"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/jackc/pgx/v5"
)

// forexMinLot is the spec Banka2025-E2E.pdf p.7 minimum quantity for a
// forex order — "minimalna veličina lota je 10". Smaller forex orders
// are rejected at CreateOrder with the spec's exact wording.
const forexMinLot int32 = 10

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
	// When set, the caller (must be a supervisor admin or the fund's
	// manager) is placing the order on behalf of an investment fund.
	// The order's owner becomes (fund.id, KindFund), the account must
	// equal fund.bank_account_id, and realized_gains writes are skipped
	// on the fill (funds are taxed at the client withdrawal boundary).
	OnBehalfOfFundID string
}

// CreateOrderResult bundles the persisted order with advisory flags
// the caller surfaces back to the user. The order is embedded so
// callers can keep reading order fields directly (`r.ID`, `r.Status`).
// ExchangeClosed mirrors the resolved market state at create time per
// spec p.57 — the order is still placed when the exchange is closed;
// the FE renders a notice.
type CreateOrderResult struct {
	*domain.Order
	ExchangeClosed bool
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
func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (*CreateOrderResult, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	// Fund-actor branch — supervisor places an order on behalf
	// of an investment fund they manage. Routes through a dedicated
	// helper that uses fund-scoped checks (holding availability + funds
	// available read against the fund row, not the supervisor).
	if in.OnBehalfOfFundID != "" {
		return s.createFundActorOrderFromInput(ctx, p, in)
	}
	if err := s.assertTraderRole(p); err != nil {
		return nil, err
	}
	if err := validateOrderShape(in); err != nil {
		return nil, err
	}
	if in.Margin {
		// Spec p.55: employees need permissions.TradingMargin; clients
		// auto-qualify if they hold any approved loan ("Klijent sa
		// odobrenim kreditom automatski dobija ovu permisiju"). We
		// honor that here instead of mutating user.permissions on loan
		// approval, so the JWT claim isn't load-bearing for the rule.
		has := permissions.Has(p.Permissions, permissions.TradingMargin)
		if !has && p.UserKind == auth.KindClient && s.MarginChecker != nil {
			_, amt, lerr := s.MarginChecker.ClientLargestActiveLoan(ctx, p.UserID)
			if lerr != nil {
				return nil, lerr
			}
			has = amt != ""
		}
		if !has {
			return nil, apperr.PermissionDenied("nedovoljne permisije za margin trgovinu")
		}
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
	// Spec Banka2025-E2E.pdf p.7: forex orders below the minimum lot
	// size are rejected with the spec's exact wording. Same gate
	// covers the c3-tests.pdf S27 "option ispod minimalne količine"
	// intent — option contracts trade 1-at-a-time so minLot=1 is the
	// effective floor there; spec gives no other type-specific lot.
	if sec.Type == domain.SecurityForex && in.Quantity < forexMinLot {
		return nil, apperr.Validation("Nalog ispod minimalne veličine lota")
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

	// Spec C3-tests S37: SELLs may not exceed current holdings.
	// Forex skips — there is no holding row (spec p.42 paired
	// settlement). Margin SELLs are short positions and would
	// require their own short-inventory machinery; the current
	// FE doesn't expose a margin SELL flow, so disallow at the
	// service edge rather than half-implement.
	if in.Direction == domain.DirectionSell && sec.Type != domain.SecurityForex && !in.Margin {
		if err := s.assertHoldingAvailable(ctx, p, in.AccountID, sec, in.Quantity); err != nil {
			return nil, err
		}
	}

	// Spec p.55: margin orders must additionally satisfy
	//   client: loan_amount > IMC OR account_balance > IMC
	//   actuary: account_balance > IMC
	// where IMC = 1.1 × maintenance_margin (in security currency).
	if in.Margin {
		if err := s.assertMarginEligible(ctx, p, in.AccountID, sec, listing, in.Quantity); err != nil {
			return nil, err
		}
	} else if in.Direction == domain.DirectionBuy && sec.Type != domain.SecurityForex {
		// Non-margin buys need a pre-fill funds check too.
		// Without it the order is accepted, the worker tries to fill,
		// the bank refuses on insufficient funds, and the order stalls
		// pending forever. Better to reject up front. Forex skips —
		// settles paired against the bank's per-currency forex_book,
		// not the user's AccountID (spec p.42).
		if err := s.assertFundsAvailable(ctx, in.AccountID, sec, in.Quantity, priceSnap, contractSize); err != nil {
			return nil, err
		}
	}

	// after_hours / exchange_closed flags: if the security trades on an
	// exchange, ask the resolver. Forex / options without an exchange
	// skip this — exchange_closed stays false there.
	afterHours := false
	exchangeClosed := false
	if sec.ExchangeMIC != "" {
		ex, err := s.Store.GetExchange(ctx, sec.ExchangeMIC)
		if err == nil {
			ms := s.resolveMarketState(ex, now)
			afterHours = ms.IsAfterHours
			// Spec p.57: notify the user when the exchange is closed.
			// The order is still accepted (orders can sit overnight); the
			// FE renders a Sonner toast off the response flag.
			exchangeClosed = !ms.IsOpen && !ms.IsAfterHours
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

	// is_actuary is captured here from the principal's permissions and
	// frozen on the row. Spec p.26 / p.55-56 use this on settle to gate
	// FX-commission policy and to pick the bank-side house leg; deriving
	// it from user_kind=='employee' over-includes any future non-actuary
	// employee.
	isActuary := permissions.HasAny(p.Permissions,
		permissions.Admin, permissions.ActuarySupervisor, permissions.ActuaryAgent)

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
		IsActuary:        isActuary,
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
	// S20: an agent order that needs supervisor approval entered Pending.
	if out.Status == domain.OrderStatusPending {
		s.notifyOrderPending(ctx, out)
	}
	return &CreateOrderResult{Order: out, ExchangeClosed: exchangeClosed}, nil
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
	// OrderType narrows to a single type (todoSpec S34); From/To bound
	// creation date inclusively (todoSpec S33). All optional.
	OrderType string
	From      *time.Time
	To        *time.Time
	Page      int
	PageSize  int
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
		OrderType:  in.OrderType,
		From:       in.From,
		To:         in.To,
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
	// Spec p.50: "Kod hartija koje imaju settlement date, i gde je taj
	// datum prošao, postoji samo Decline opcija." Auto-decline if the
	// security's settlement date passed between create and approve.
	sec, err := s.Store.GetSecurity(ctx, cur.SecurityID)
	if err != nil {
		return nil, err
	}
	if sec.SettlementDate != nil && !sec.SettlementDate.After(s.now()) {
		declined, derr := s.Store.DeclineOrder(ctx, id, p.UserID)
		if derr != nil {
			return nil, derr
		}
		s.Log.Info("auto-declined approval — security past settlement date",
			"order_id", id, "security_id", cur.SecurityID, "settlement", sec.SettlementDate)
		// S25: system auto-cancelled the order because the security expired.
		s.notifyOrderAutoCancelled(ctx, declined, "hartija je istekla pre izvršenja")
		return declined, apperr.FailedPrecondition("hartija je istekla — nalog je automatski odbijen")
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
	// S21: supervisor approved the order.
	s.notifyOrderApproved(ctx, out)
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
	out, err := s.Store.DeclineOrder(ctx, id, p.UserID)
	if err != nil {
		return nil, err
	}
	// S22: supervisor declined the order.
	s.notifyOrderDeclined(ctx, out)
	return out, nil
}

// CancelOrder marks the order cancelled. Only the order's owner (or a
// supervisor/admin) can cancel. Cancelling halts further fills; fills
// that already settled stay sealed (spec p.50).
//
// Spec p.57: "otkazivanje celog ili dela Order-a". When partialQty is
// 0 or >= remaining_quantity, the whole order is cancelled. When
// 0 < partialQty < remaining_quantity, the order's target + remaining
// drop by partialQty and the order keeps trading toward the smaller
// target — already-filled portions are honoured.
//
// Spec p.38 reads "transakcija" as a trade — cancelling an approved
// order that hasn't fully filled hands the agent's daily capacity back.
// Refund is the cancelled-portion's RSD-equivalent; clamped at 0
// store-side so a refund landing after the daily reset cron doesn't
// underflow.
func (s *Service) CancelOrder(ctx context.Context, id string, partialQty int32) (*domain.Order, error) {
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
	// partialQty > remaining is a user error — refuse rather than
	// silently degrading to a full cancel.
	if partialQty > 0 && partialQty > cur.RemainingQuantity {
		return nil, apperr.Validation("količina prevazilazi preostalu količinu naloga")
	}
	var (
		out          *domain.Order
		cancelledQty int32
		fullCancel   = partialQty == 0 || partialQty >= cur.RemainingQuantity
	)
	if fullCancel {
		out, err = s.Store.CancelOrder(ctx, id)
		if err != nil {
			return nil, err
		}
		cancelledQty = cur.RemainingQuantity
	} else {
		out, err = s.Store.PartialCancelOrder(ctx, id, partialQty)
		if err != nil {
			return nil, err
		}
		cancelledQty = partialQty
	}
	if cur.Status == domain.OrderStatusApproved && cur.UserKind == domain.KindEmployee && cur.IsActuary {
		s.maybeRefundAgentLimitQty(ctx, cur, cancelledQty)
	}
	return out, nil
}

// =====================================================================
// Margin eligibility helpers (spec p.55)
// =====================================================================

// assertMarginEligible enforces the spec p.55 funding-source check.
// Both clients and actuaries pass on (account_available > IMC).
// Clients additionally pass on (largest_active_loan > IMC). All amounts
// are normalised to RSD via the rate provider so cross-currency
// accounts and loans are handled uniformly. With no MarginChecker
// wired (minimal dev stack) the check degrades to permission-only and
// logs a warning.
func (s *Service) assertMarginEligible(
	ctx context.Context,
	p auth.Principal,
	accountID string,
	sec *domain.Security,
	listing *domain.Listing,
	qty int32,
) error {
	if s.MarginChecker == nil {
		s.Log.Warn("margin checker not wired; skipping spec p.55 funding check",
			"account_id", accountID, "security_id", sec.ID)
		return nil
	}
	imcRSD, ok, err := s.initialMarginCostRSD(ctx, sec, listing, qty)
	if err != nil {
		return err
	}
	if !ok {
		// Couldn't compute IMC (no listing/premium price) — spec implies
		// margin shouldn't be possible without a known price. Reject.
		return apperr.FailedPrecondition("nije moguće izračunati Initial Margin Cost za ovu hartiju")
	}

	cur, avail, err := s.MarginChecker.AccountAvailable(ctx, accountID)
	if err != nil {
		return err
	}
	availRSD, err := s.amountToRSD(ctx, cur, avail)
	if err != nil {
		return err
	}
	if availRSD.Cmp(imcRSD) > 0 {
		return nil
	}

	if p.UserKind == auth.KindClient {
		loanCur, loanAmt, err := s.MarginChecker.ClientLargestActiveLoan(ctx, p.UserID)
		if err != nil {
			return err
		}
		if loanAmt != "" {
			loanRSD, err := s.amountToRSD(ctx, loanCur, loanAmt)
			if err != nil {
				return err
			}
			if loanRSD.Cmp(imcRSD) > 0 {
				return nil
			}
		}
	}
	return apperr.FailedPrecondition("Initial Margin Cost prelazi raspoloživa sredstva i dostupne kredite")
}

// assertFundsAvailable enforces a balance ≥ trade-notional check on
// non-margin buys. Comparison is in RSD via the rate provider's ASK
// (no commission), mirroring the margin path so cross-currency
// accounts/securities behave the same way. With no MarginChecker
// wired (minimal dev stack) the check degrades to a noop with a
// warning, same as assertMarginEligible.
//
// We don't add commission here: the cap is small relative to the
// notional and the bank tx-tolerance has a few thousandths of slack
// on the per-fill commission. If a buy fails mid-execution because
// commission tipped the balance over, the worker stalls — same
// recovery path as any other settle failure.
func (s *Service) assertFundsAvailable(
	ctx context.Context,
	accountID string,
	sec *domain.Security,
	qty int32,
	pricePerUnit, contractSize string,
) error {
	if s.MarginChecker == nil {
		s.Log.Warn("margin checker not wired; skipping pre-fill funds check",
			"account_id", accountID, "security_id", sec.ID)
		return nil
	}
	notionalRSD, err := s.tradeValueRSD(ctx, sec, qty, pricePerUnit, contractSize)
	if err != nil {
		return err
	}
	cur, avail, err := s.MarginChecker.AccountAvailable(ctx, accountID)
	if err != nil {
		// Bank-side lookup unavailable (dev stub or transient failure).
		// Degrade to no pre-check rather than blocking trade flow; the
		// bank's SettleTrade still rejects on insufficient funds at fill
		// time, so the worst case is a stalled order. Production wires a
		// real adapter so the check fires.
		s.Log.Warn("pre-fill funds check: account lookup failed; skipping",
			"account_id", accountID, "err", err.Error())
		return nil
	}
	availRSD, err := s.amountToRSD(ctx, cur, avail)
	if err != nil {
		return err
	}
	if availRSD.Cmp(notionalRSD) < 0 {
		return apperr.FailedPrecondition("nedovoljna sredstva na računu za ovaj nalog")
	}
	return nil
}

// assertHoldingAvailable enforces "ne mozeš prodati više nego što
// poseduješ" (spec C3-tests S37). Inventory is keyed by
// (user_id, security_id, account_id) so a user with the same security
// across two accounts must SELL from the right one. Open SELLs already
// in flight aren't subtracted: at-most-once exec means partial fills
// settle in order, and a follow-up SELL whose total exceeds the
// remaining real qty will be caught by ApplySellFill at fill time
// (returns NotFound). The pre-check prevents the simple "user
// fat-fingers 15 instead of 5" path that the spec calls out.
func (s *Service) assertHoldingAvailable(
	ctx context.Context,
	p auth.Principal,
	accountID string,
	sec *domain.Security,
	qty int32,
) error {
	hs, err := s.Store.ListHoldings(ctx, store.HoldingFilter{
		UserID:     p.UserID,
		UserKind:   domain.UserKind(p.UserKind),
		SecurityID: sec.ID,
	})
	if err != nil {
		return err
	}
	var have int32
	for _, h := range hs {
		if h.AccountID == accountID {
			have += h.Quantity
		}
	}
	if have < qty {
		return apperr.FailedPrecondition("ne možete prodati više hartija nego što posedujete")
	}
	return nil
}

// initialMarginCostRSD = qty × 1.1 × maintenance_margin, converted to
// RSD. Returns (rsd, true) on success, (nil, false) when the security
// has no usable price.
func (s *Service) initialMarginCostRSD(
	ctx context.Context,
	sec *domain.Security,
	listing *domain.Listing,
	qty int32,
) (*big.Rat, bool, error) {
	mm, ok := computeMaintenanceMargin(sec, listing)
	if !ok {
		return nil, false, nil
	}
	imc := money.Mul(mm, money.MustParse("1.1"))
	q := new(big.Rat).SetInt64(int64(qty))
	imc = money.Mul(imc, q)
	cur := sec.Currency
	if cur == "" {
		cur = domain.CurrencyRSD
	}
	rsd, err := s.amountToRSD(ctx, cur, money.FormatAmount(imc))
	if err != nil {
		return nil, false, err
	}
	return rsd, true, nil
}

// amountToRSD converts amount-in-cur to RSD via the rate provider's
// ASK with no commission. Falls back to the raw amount when cur is
// already RSD or when no rate provider is wired.
func (s *Service) amountToRSD(ctx context.Context, cur domain.Currency, amount string) (*big.Rat, error) {
	r, err := money.Parse(amount)
	if err != nil {
		return nil, apperr.Internal("amount unparseable", err)
	}
	if cur == "" || cur == domain.CurrencyRSD {
		return r, nil
	}
	if s.Rates == nil {
		return r, nil
	}
	_, ask, err := s.Rates.Quote(ctx, cur, domain.CurrencyRSD)
	if err != nil {
		return nil, apperr.Internal("fx quote failed", err)
	}
	rate, err := money.Parse(ask)
	if err != nil {
		return nil, apperr.Internal("fx ask unparseable", err)
	}
	return money.Mul(r, rate), nil
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
		// Spec p.38 assumes every agent has a row with a definite limit.
		// A missing row is a misconfiguration — refuse rather than
		// flooding the supervisor queue with arbitrarily large pending
		// orders that would still be auto-charged on approval.
		return false, false, apperr.FailedPrecondition("aktuar nije konfigurisan — kontaktirajte supervizora")
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
	// Spec p.38 reserves daily_limit=0 for supervisors (who have no cap).
	// For an agent, 0 means zero capacity — every trade routes to the
	// supervisor.
	if info.Type == domain.ActuaryAgent && limit.Sign() == 0 {
		return false, true, nil
	}
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

// maybeRefundAgentLimit hands an agent's daily capacity back when an
// approved order is cancelled before all fills landed. The refund uses
// the create-time RSD-equivalent (same number that was charged on
// approval) so charge + refund net to zero on the typical case.
// Refund is clamped at 0 store-side: if the daily reset cron has
// already zeroed used_limit between approval and cancel, the
// constraint stays intact. Failure is logged, not propagated — the
// order is already cancelled and the cap is best-effort.
// maybeRefundAgentLimitQty hands the agent's daily capacity back for
// the cancelled portion of an order (spec p.38). For a full cancel the
// caller passes o.RemainingQuantity; for a partial cancel only the
// dropped slice. Store-side clamp keeps the refund from underflowing
// after the daily reset cron.
func (s *Service) maybeRefundAgentLimitQty(ctx context.Context, o *domain.Order, qty int32) {
	if qty <= 0 {
		return
	}
	sec, err := s.Store.GetSecurity(ctx, o.SecurityID)
	if err != nil {
		s.Log.Warn("limit refund: security lookup failed", "order_id", o.ID, "err", err.Error())
		return
	}
	rsd, err := s.tradeValueRSD(ctx, sec, qty, o.PricePerUnit, o.ContractSize)
	if err != nil {
		s.Log.Warn("limit refund: rsd math failed", "order_id", o.ID, "err", err.Error())
		return
	}
	if rsd.Sign() == 0 {
		return
	}
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		return s.Store.RefundUsedLimit(ctx, tx, o.UserID, money.FormatAmount(rsd))
	}); err != nil {
		s.Log.Warn("limit refund: db update failed", "order_id", o.ID, "err", err.Error())
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

// =====================================================================
// Fund-actor order placement (spec p.74-75)
// =====================================================================

// fundActorOrderInput is the internal helper input. Used by both the
// public CreateOrder fund-actor branch and the auto-liquidation step of
// the fund_withdraw saga.
type fundActorOrderInput struct {
	FundID     string
	SecurityID string
	AccountID  string
	Quantity   int32
	Direction  domain.Direction
	OrderType  domain.OrderType
	LimitPrice string
	StopPrice  string
	AllOrNone  bool
	// InitiatorUser is the supervisor who initiated. May be empty when
	// the saga's own context owns the call (recovery worker re-enters).
	InitiatorUser string
}

// createFundActorOrderFromInput is the CreateOrder fund-actor branch.
// Validates the caller is the fund's manager + the account matches the
// fund's bank account, then delegates to createFundActorOrder.
func (s *Service) createFundActorOrderFromInput(
	ctx context.Context, p auth.Principal, in CreateOrderInput,
) (*CreateOrderResult, error) {
	if err := s.requireFundsManage(p); err != nil {
		return nil, err
	}
	f, err := s.Store.GetFund(ctx, in.OnBehalfOfFundID)
	if err != nil {
		return nil, err
	}
	if err := requireFundManager(p, f); err != nil {
		return nil, err
	}
	if in.AccountID != f.BankAccountID {
		return nil, apperr.Validation("nalog mora ići preko računa fonda")
	}
	if in.Margin {
		return nil, apperr.Validation("fond ne podržava margin trgovinu")
	}
	o, err := s.createFundActorOrder(ctx, fundActorOrderInput{
		FundID:        f.ID,
		SecurityID:    in.SecurityID,
		AccountID:     in.AccountID,
		Quantity:      in.Quantity,
		Direction:     in.Direction,
		OrderType:     in.OrderType,
		LimitPrice:    in.LimitPrice,
		StopPrice:     in.StopPrice,
		AllOrNone:     in.AllOrNone,
		InitiatorUser: p.UserID,
	})
	if err != nil {
		return nil, err
	}
	return &CreateOrderResult{Order: o}, nil
}

// createFundActorOrder is the shared insertion path used by the
// fund-actor branch of CreateOrder and the auto-liquidation step of
// the fund_withdraw saga. Skips the agent-limit / client-only
// instrument checks (those are caller-scoped concerns); always
// auto-approves (a supervisor placing on behalf of the bank doesn't
// need themselves to approve their own decision).
func (s *Service) createFundActorOrder(ctx context.Context, in fundActorOrderInput) (*domain.Order, error) {
	if in.Quantity <= 0 {
		return nil, apperr.Validation("količina mora biti pozitivna")
	}
	sec, err := s.Store.GetSecurity(ctx, in.SecurityID)
	if err != nil {
		return nil, err
	}
	if sec.Type == domain.SecurityForex || sec.Type == domain.SecurityOption {
		// Funds settle through MARKET sells against listed stocks/futures.
		return nil, apperr.Validation("fond ne podržava ovaj tip hartije")
	}
	if sec.SettlementDate != nil && !sec.SettlementDate.After(s.now()) {
		return nil, apperr.FailedPrecondition("hartija je istekla — trgovina nije moguća")
	}
	listing, err := s.Store.GetListingBySecurityID(ctx, in.SecurityID)
	if err != nil {
		return nil, err
	}
	priceSnap := listing.Price
	contractSize := listing.ContractSize
	if contractSize == "" {
		contractSize = "1"
	}
	// SELL: assert the fund's holding covers qty.
	if in.Direction == domain.DirectionSell {
		hs, err := s.Store.ListHoldings(ctx, store.HoldingFilter{
			UserID: in.FundID, UserKind: domain.KindFund, SecurityID: sec.ID,
		})
		if err != nil {
			return nil, err
		}
		var have int32
		for _, h := range hs {
			if h.AccountID == in.AccountID {
				have += h.Quantity
			}
		}
		if have < in.Quantity {
			return nil, apperr.FailedPrecondition("fond nema dovoljno hartija za prodaju")
		}
	}
	// BUY: assert the fund's bank account has enough RSD for the trade.
	if in.Direction == domain.DirectionBuy {
		if err := s.assertFundsAvailable(ctx, in.AccountID, sec, in.Quantity, priceSnap, contractSize); err != nil {
			return nil, err
		}
	}
	afterHours := false
	if sec.ExchangeMIC != "" {
		if ex, err := s.Store.GetExchange(ctx, sec.ExchangeMIC); err == nil {
			ms := s.resolveMarketState(ex, s.now())
			afterHours = ms.IsAfterHours
		}
	}
	o := &domain.Order{
		UserID:           in.FundID,
		UserKind:         domain.KindFund,
		ActorKind:        domain.KindFund,
		OnBehalfOfFundID: in.FundID,
		SecurityID:       in.SecurityID,
		OrderType:        in.OrderType,
		Direction:        in.Direction,
		Quantity:         in.Quantity,
		ContractSize:     contractSize,
		PricePerUnit:     priceSnap,
		LimitPrice:       in.LimitPrice,
		StopPrice:        in.StopPrice,
		AllOrNone:        in.AllOrNone,
		IsActuary:        true, // bank-actor → no FX commission (spec p.55)
		AccountID:        in.AccountID,
		Status:           domain.OrderStatusApproved,
		ApprovalRequired: false,
		AfterHours:       afterHours,
		ApprovedBy:       in.InitiatorUser,
	}
	return s.Store.CreateOrder(ctx, o)
}
