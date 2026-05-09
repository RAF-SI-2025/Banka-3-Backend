// Execution: turns approved orders into a stream of partial fills,
// settles each fill via the bank, and updates the portfolio.
//
// Spec p.55-56 cadence (paraphrased):
//   * Each fill takes a random sub-quantity of the remaining qty.
//   * The interval between fills is roughly proportional to listing
//     volume — fewer fills per minute on a thinly-traded security.
//     Formula used: interval_minutes = Random(0, 1440 / (volume / remaining))
//     so as remaining shrinks, the cap grows; as volume grows, the
//     cap shrinks.
//   * After-hours (within 4h of close): add 30 min to each interval.
//
// The worker (see app/jobs.go) wakes up every tickInterval and asks
// each active order whether it should fire one fill. The decision +
// the actual fill go through ProcessOrderTick below.

package service

import (
	"context"
	"errors"
	"math/big"
	"math/rand"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SettleInput is the payload to TradeSettler.Settle. Fields mirror the
// bank.SettleTrade RPC.
type SettleInput struct {
	AccountID string
	Direction string // "debit" (buy) | "credit" (sell)
	Currency  domain.Currency
	Amount    string // already includes commission + fee; bank just moves money
	OpID      string
	IsActuary bool
	Purpose   string
}

// TradeSettler is the trading service's view of the bank's settlement
// surface. The app layer wires this to bank.SettleTrade; tests inject
// a stub.
type TradeSettler interface {
	Settle(ctx context.Context, in SettleInput) (string, error)
}

// rand source — package-level so tests can swap it.
var executionRand = rand.New(rand.NewSource(time.Now().UnixNano()))

// processFillResult is the post-state returned from one fill.
type processFillResult struct {
	Fired       bool
	Order       *domain.Order
	Execution   *domain.OrderExecution
	NextEarliest time.Time
}

// ProcessOrderTick is invoked by the execution worker for one active
// order. Returns whether a fill fired, and (when none fires) the
// earliest time the worker should retry this order. Fillability is
// gated on:
//   - cancelled / done flags (worker shouldn't pick these but defend);
//   - STOP / STOP_LIMIT trigger condition vs current price;
//   - LIMIT price condition vs current ask (buy) / bid (sell);
//   - cadence based on spec p.56 random-interval formula.
func (s *Service) ProcessOrderTick(ctx context.Context, o *domain.Order) (processFillResult, error) {
	res := processFillResult{Order: o}
	if o.Cancelled || o.IsDone || o.Status != domain.OrderStatusApproved {
		return res, nil
	}
	listing, err := s.Store.GetListingBySecurityID(ctx, o.SecurityID)
	if err != nil {
		// Options without a listing fall back to the security's premium.
		sec, err2 := s.Store.GetSecurity(ctx, o.SecurityID)
		if err2 == nil && sec.Type == domain.SecurityOption && sec.Premium != "" {
			listing = &domain.Listing{
				SecurityID:   sec.ID,
				Price:        sec.Premium,
				Ask:          sec.Premium,
				Bid:          sec.Premium,
				ContractSize: sec.ContractSize,
				Volume:       1000, // synthetic volume for cadence math
			}
		} else {
			return res, err
		}
	}

	// Trigger detection for STOP / STOP_LIMIT.
	if (o.OrderType == domain.OrderStop || o.OrderType == domain.OrderStopLimit) && !o.Triggered {
		if !s.stopTriggered(o, listing) {
			res.NextEarliest = s.now().Add(s.tickRetryInterval())
			return res, nil
		}
		// Persist the trigger flag and continue.
		if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
			return s.Store.SetOrderTriggered(ctx, tx, o.ID)
		}); err != nil {
			return res, err
		}
		o.Triggered = true
	}

	// Execution price: per the order type.
	var fillPrice string
	switch effectiveType(o) {
	case domain.OrderMarket:
		// Market: take the touch — ask for buy, bid for sell.
		if o.Direction == domain.DirectionBuy {
			fillPrice = listing.Ask
		} else {
			fillPrice = listing.Bid
		}
	case domain.OrderLimit:
		// Limit: only fills when the market crosses the limit.
		if !s.limitConditionMet(o, listing) {
			res.NextEarliest = s.now().Add(s.tickRetryInterval())
			return res, nil
		}
		// Fill at the limit price (worst case for the trader).
		fillPrice = o.LimitPrice
	default:
		return res, apperr.Internal("unknown effective order type", nil)
	}

	// Cadence: did we wait long enough since the last fill?
	if !s.cadenceReady(ctx, o, listing) {
		res.NextEarliest = s.now().Add(s.tickRetryInterval())
		return res, nil
	}

	// Sub-quantity: AON forces whole-order fill, others pick a random
	// chunk in [1, remaining].
	subQty := o.RemainingQuantity
	if !o.AllOrNone && o.RemainingQuantity > 1 {
		subQty = int32(executionRand.Intn(int(o.RemainingQuantity))) + 1
	}

	// Execute the fill.
	exec, err := s.executeFill(ctx, o, listing, fillPrice, subQty)
	if err != nil {
		return res, err
	}
	res.Fired = true
	res.Execution = exec
	return res, nil
}

// executeFill performs one partial-fill pipeline atomically:
//   1. Bank settles the cash leg (debit on buy / credit on sell).
//   2. Inside a tx: insert order_executions, advance order progress,
//      apply portfolio change, write realized_gain on a sell.
//
// Failures during settlement abort early. Failures after settlement
// log and rely on retry — bank has the money moved; we owe an
// execution row. The op_id idempotency key on the bank side prevents
// a double-charge when we retry.
func (s *Service) executeFill(ctx context.Context, o *domain.Order, listing *domain.Listing, fillPrice string, qty int32) (*domain.OrderExecution, error) {
	if s.Settler == nil {
		return nil, apperr.Internal("trade settler not wired", nil)
	}

	contractSize := o.ContractSize
	if contractSize == "" {
		contractSize = listing.ContractSize
	}
	if contractSize == "" {
		contractSize = "1"
	}

	csR, err := money.Parse(contractSize)
	if err != nil {
		return nil, apperr.Internal("contract size unparseable", err)
	}
	priceR, err := money.Parse(fillPrice)
	if err != nil {
		return nil, apperr.Internal("fill price unparseable", err)
	}
	qtyR := new(big.Rat).SetInt64(int64(qty))
	notional := money.Mul(money.Mul(qtyR, csR), priceR)
	commission := s.commissionFor(o, notional)

	var settleAmount *big.Rat
	direction := "debit"
	switch o.Direction {
	case domain.DirectionBuy:
		settleAmount = money.Add(notional, commission) // user pays principal + commission
		direction = "debit"
	case domain.DirectionSell:
		settleAmount = money.Sub(notional, commission) // user receives principal - commission
		direction = "credit"
	}
	if !money.IsPositive(settleAmount) {
		return nil, apperr.Validation("ukupan iznos posle provizije nije pozitivan")
	}

	sec, err := s.Store.GetSecurity(ctx, o.SecurityID)
	if err != nil {
		return nil, err
	}
	currency := sec.Currency
	if currency == "" {
		currency = domain.CurrencyRSD
	}

	opID := uuid.NewString()
	settledOpID, err := s.Settler.Settle(ctx, SettleInput{
		AccountID: o.AccountID,
		Direction: direction,
		Currency:  currency,
		Amount:    money.FormatAmount(settleAmount),
		OpID:      opID,
		IsActuary: o.UserKind == domain.KindEmployee,
		Purpose:   "Fill " + sec.Ticker,
	})
	if err != nil {
		return nil, err
	}

	var exec *domain.OrderExecution
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		e, err := s.Store.InsertExecution(ctx, tx, &domain.OrderExecution{
			OrderID:       o.ID,
			Quantity:      qty,
			PricePerUnit:  fillPrice,
			TotalAmount:   money.FormatAmount(notional),
			CommissionAmt: money.FormatAmount(commission),
			BankOpID:      settledOpID,
		})
		if err != nil {
			return err
		}
		exec = e
		if _, err := s.Store.AdvanceOrderProgress(ctx, tx, o.ID, qty); err != nil {
			return err
		}

		switch o.Direction {
		case domain.DirectionBuy:
			if _, err := s.Store.ApplyBuyFill(ctx, tx,
				o.UserID, string(o.UserKind), o.SecurityID, o.AccountID,
				qty, fillPrice,
			); err != nil {
				return err
			}
		case domain.DirectionSell:
			avgPrice, _, err := s.Store.ApplySellFill(ctx, tx,
				o.UserID, string(o.UserKind), o.SecurityID, o.AccountID, qty,
			)
			if err != nil {
				return err
			}
			if err := s.recordRealizedGain(ctx, tx, o, sec, qty, fillPrice, avgPrice, contractSize); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.Log.Error("fill book-keeping failed after bank settle",
			"order_id", o.ID, "op_id", settledOpID, "err", err.Error())
		return nil, err
	}
	return exec, nil
}

// recordRealizedGain writes one row to realized_gains per spec p.62.
// gain_native = (sellPrice - costBasis) * qty * contractSize, in the
// security's currency. gain_rsd = gain_native converted to RSD via
// the rate provider (no commission); falls through to gain_native
// when the security is already RSD or rates aren't wired.
func (s *Service) recordRealizedGain(
	ctx context.Context, tx pgx.Tx,
	o *domain.Order, sec *domain.Security,
	qty int32, sellPrice, costBasis, contractSize string,
) error {
	sell, err := money.Parse(sellPrice)
	if err != nil {
		return apperr.Internal("sell price unparseable", err)
	}
	cost, err := money.Parse(costBasis)
	if err != nil {
		return apperr.Internal("cost basis unparseable", err)
	}
	cs, err := money.Parse(contractSize)
	if err != nil {
		return apperr.Internal("contract size unparseable", err)
	}
	q := new(big.Rat).SetInt64(int64(qty))
	gainNativePerUnit := money.Sub(sell, cost)
	gainNative := money.Mul(money.Mul(q, cs), gainNativePerUnit)

	cur := sec.Currency
	if cur == "" {
		cur = domain.CurrencyRSD
	}

	var gainRSD *big.Rat
	if cur == domain.CurrencyRSD || s.Rates == nil {
		gainRSD = new(big.Rat).Set(gainNative)
	} else {
		_, ask, err := s.Rates.Quote(ctx, cur, domain.CurrencyRSD)
		if err == nil {
			r, err := money.Parse(ask)
			if err == nil {
				gainRSD = money.Mul(gainNative, r)
			}
		}
		if gainRSD == nil {
			s.Log.Warn("rsd conversion for realized gain failed; using native value",
				"order_id", o.ID, "currency", cur)
			gainRSD = new(big.Rat).Set(gainNative)
		}
	}

	costBasisAmt := money.Mul(money.Mul(q, cs), cost)
	proceedsAmt := money.Mul(money.Mul(q, cs), sell)

	_, err = s.Store.InsertRealizedGain(ctx, tx, &domain.RealizedGain{
		UserID:       o.UserID,
		UserKind:     o.UserKind,
		SecurityID:   o.SecurityID,
		AccountID:    o.AccountID,
		Quantity:     qty,
		CostBasisAmt: money.FormatAmount(costBasisAmt),
		ProceedsAmt:  money.FormatAmount(proceedsAmt),
		Currency:     cur,
		GainNative:   money.FormatAmount(gainNative),
		GainRSD:      money.FormatAmount(gainRSD),
	})
	return err
}

// stopTriggered evaluates the stop condition.
//
// Buy stop: triggers when the market last_price >= stop_price (price
// rose to/through the threshold). Sell stop: triggers when last_price
// <= stop_price.
func (s *Service) stopTriggered(o *domain.Order, listing *domain.Listing) bool {
	stop, err := money.Parse(o.StopPrice)
	if err != nil {
		return false
	}
	last, err := money.Parse(listing.Price)
	if err != nil {
		return false
	}
	if o.Direction == domain.DirectionBuy {
		return last.Cmp(stop) >= 0
	}
	return last.Cmp(stop) <= 0
}

// limitConditionMet evaluates whether the market is willing to fill
// this limit order at its limit price.
//
// Buy limit fills when ask <= limit_price. Sell limit fills when
// bid >= limit_price.
func (s *Service) limitConditionMet(o *domain.Order, listing *domain.Listing) bool {
	limit, err := money.Parse(o.LimitPrice)
	if err != nil {
		return false
	}
	switch o.Direction {
	case domain.DirectionBuy:
		ask, err := money.Parse(listing.Ask)
		if err != nil {
			return false
		}
		return ask.Cmp(limit) <= 0
	case domain.DirectionSell:
		bid, err := money.Parse(listing.Bid)
		if err != nil {
			return false
		}
		return bid.Cmp(limit) >= 0
	}
	return false
}

// effectiveType maps a triggered STOP/STOP_LIMIT into its post-trigger
// type per spec p.50: STOP becomes Market, STOP_LIMIT becomes Limit.
func effectiveType(o *domain.Order) domain.OrderType {
	if !o.Triggered {
		return o.OrderType
	}
	switch o.OrderType {
	case domain.OrderStop:
		return domain.OrderMarket
	case domain.OrderStopLimit:
		return domain.OrderLimit
	}
	return o.OrderType
}

// cadenceReady decides whether enough time has passed since the last
// fill for this order to fire another. Per spec p.56:
//
//   maxIntervalMinutes = 1440 / (volume / remaining)
//   intervalMinutes    = Random(0, maxIntervalMinutes)
//   if afterHours: intervalMinutes += 30
//
// We compare since-last-fill (or since-creation when no fills yet) to
// the freshly-rolled interval. This randomness is on every tick —
// equivalent in expectation to a single roll then waiting, but
// stateless on the worker side.
func (s *Service) cadenceReady(ctx context.Context, o *domain.Order, listing *domain.Listing) bool {
	if o.AllOrNone {
		// AON has no partial pacing — fire as soon as conditions allow.
		// Spec doesn't say otherwise; without random pacing AON fills
		// on the first ready tick after approval.
		return true
	}

	volume := listing.Volume
	if volume <= 0 {
		volume = 1
	}
	remaining := int64(o.RemainingQuantity)
	if remaining <= 0 {
		return false
	}
	// 1440 minutes per day / fills_per_remaining
	// fills_per_remaining = volume / remaining
	// max_interval        = 1440 / fills_per_remaining = 1440 * remaining / volume
	maxInterval := time.Duration(1440*remaining/volume) * time.Minute
	if maxInterval < 5*time.Second {
		maxInterval = 5 * time.Second // floor for tests / extreme volume
	}
	if o.AfterHours {
		maxInterval += 30 * time.Minute
	}

	interval := time.Duration(executionRand.Int63n(int64(maxInterval)))
	since, ok, err := s.timeSinceLastFill(ctx, o)
	if err != nil {
		s.Log.Warn("cadence: latest exec lookup failed", "order_id", o.ID, "err", err.Error())
		return false
	}
	// If no fills yet, anchor to last_modification (or created_at).
	if !ok {
		anchor := o.LastModification
		if anchor.IsZero() {
			anchor = o.CreatedAt
		}
		since = s.now().Sub(anchor)
	}
	return since >= interval
}

// timeSinceLastFill returns the duration since the latest execution on
// the order, plus an "ok" boolean that's false when no fills exist yet.
func (s *Service) timeSinceLastFill(ctx context.Context, o *domain.Order) (time.Duration, bool, error) {
	tStr, err := s.Store.LatestExecutionAt(ctx, o.ID)
	if err != nil {
		return 0, false, err
	}
	if tStr == "" {
		return 0, false, nil
	}
	t, err := time.Parse("2006-01-02 15:04:05.999999-07", tStr)
	if err != nil {
		// Different driver renderings can include different layouts;
		// Postgres' default is fine but if the column ever returns ISO
		// 8601 try that too.
		t2, err2 := time.Parse(time.RFC3339Nano, tStr)
		if err2 != nil {
			return 0, false, errors.Join(err, err2)
		}
		t = t2
	}
	return s.now().Sub(t), true, nil
}

// commissionFor returns the per-fill commission per spec p.55-56.
//
//   Market    : min(14% * notional, $7 in security currency)
//   Limit     : min(24% * notional, $12 in security currency)
//   Stop      : same as Market once triggered
//   StopLimit : same as Limit once triggered
//
// The "$7"/"$12" caps are denominated in the security currency for
// simplicity — the spec uses dollar caps but we honor them as a
// per-currency unit-cap (no FX hop).
//
// Actuary / supervisor employees trading on behalf of the bank pay
// the same trade commission as clients per spec p.55-56; only the
// FX leg is commission-free for them (handled bank-side).
func (s *Service) commissionFor(o *domain.Order, notional *big.Rat) *big.Rat {
	var rateStr, capStr string
	switch effectiveType(o) {
	case domain.OrderMarket:
		rateStr, capStr = "0.14", "7"
	case domain.OrderLimit:
		rateStr, capStr = "0.24", "12"
	default:
		rateStr, capStr = "0.14", "7"
	}
	rate, _ := money.Parse(rateStr)
	cap, _ := money.Parse(capStr)
	pct := money.Mul(notional, rate)
	if pct.Cmp(cap) < 0 {
		return pct
	}
	return cap
}

// tickRetryInterval is how long to wait before re-checking an order
// that wasn't ready this tick. Tests pin Service.Now and shrink this
// via a config knob if needed; the default keeps the worker quiet.
func (s *Service) tickRetryInterval() time.Duration {
	if s.Cfg.TickRetry > 0 {
		return s.Cfg.TickRetry
	}
	return 5 * time.Second
}

// RunExecutionTick walks every active order once. Used by the worker
// loop and exposed for forced runs (debug / test). Returns the number
// of fills fired.
func (s *Service) RunExecutionTick(ctx context.Context) (int, error) {
	orders, err := s.Store.GetActiveOrdersForExecution(ctx, 200)
	if err != nil {
		return 0, err
	}
	fired := 0
	for _, o := range orders {
		res, err := s.ProcessOrderTick(ctx, o)
		if err != nil {
			s.Log.Warn("order tick failed", "order_id", o.ID, "err", err.Error())
			continue
		}
		if res.Fired {
			fired++
		}
	}
	return fired, nil
}

// IsActuaryOrder reports whether `o` was placed by an actuary (used
// for FX-commission policy in bank settlement). Kept here so the
// worker doesn't reach into pkg/permissions for one boolean.
func IsActuaryOrder(o *domain.Order) bool {
	return o.UserKind == domain.KindEmployee
}

// (Re-export for the unit test file's vis on the helper.)
var _ = func() bool {
	// touch unused imports if any sneak in during refactor
	_ = auth.KindEmployee
	_ = permissions.Actuary
	return true
}()
