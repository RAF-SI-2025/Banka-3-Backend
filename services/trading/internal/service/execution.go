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

// SettleForexInput pairs the two cash legs of a spec p.42 forex fill.
// Direction is "buy" or "sell" of the base currency.
type SettleForexInput struct {
	Direction     string
	BaseCurrency  domain.Currency
	BaseAmount    string
	QuoteCurrency domain.Currency
	QuoteAmount   string
	OpID          string
	Purpose       string
}

// TradeSettler is the trading service's view of the bank's settlement
// surface. The app layer wires this to bank.SettleTrade; tests inject
// a stub.
type TradeSettler interface {
	Settle(ctx context.Context, in SettleInput) (string, error)
}

// ForexSettler is the trading service's view of bank.SettleForexFill.
// May be nil on a minimal dev stack — in that case forex orders skip
// the cash leg with a logged warning. Production wires this.
type ForexSettler interface {
	SettleForex(ctx context.Context, in SettleForexInput) (string, error)
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
		// Spec p.51: buy fills at min(limit, ask), sell fills at
		// max(limit, bid) — i.e. the trader gets the better of their
		// limit and the current touch.
		fillPrice = limitFillPrice(o, listing)
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
	sec, err := s.Store.GetSecurity(ctx, o.SecurityID)
	if err != nil {
		return nil, err
	}
	totalCommission, err := s.commissionFor(ctx, o, sec, notional)
	if err != nil {
		return nil, err
	}
	commission := proratedCommission(totalCommission, qty, o.Quantity)

	// Forex orders use the spec p.42 paired-settlement RPC instead of
	// the user-vs-house SettleTrade flow. Two legs go through the
	// bank's per-currency forex_book accounts atomically.
	settledOpID, err := s.settleCashLeg(ctx, o, sec, fillPrice, qty, notional, commission, csR)
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

		// Spec p.42: forex pairs are not held — buying a pair means
		// selling one currency and buying the other, with no portfolio
		// position. Skip portfolio + realized-gain bookkeeping here.
		// TODO(c3 forex): the cash leg is currently still a single-
		// currency SettleTrade above; proper two-leg paired settlement
		// (debit quote-currency account, credit base-currency account)
		// needs a new bank-side primitive — tracked separately.
		if sec.Type == domain.SecurityForex {
			return nil
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

// settleCashLeg routes the cash leg of a fill to the right bank
// primitive. Stocks/futures/options use SettleTrade against the user
// account; forex uses SettleForexFill against the bank's per-currency
// forex_book accounts (spec p.42). Returns the bank-side op_id to
// stamp on the execution row.
func (s *Service) settleCashLeg(
	ctx context.Context,
	o *domain.Order,
	sec *domain.Security,
	fillPrice string,
	qty int32,
	notional, commission, contractSize *big.Rat,
) (string, error) {
	if sec.Type == domain.SecurityForex {
		return s.settleForexLeg(ctx, o, sec, fillPrice, qty, contractSize)
	}

	currency := sec.Currency
	if currency == "" {
		currency = domain.CurrencyRSD
	}
	var settleAmount *big.Rat
	direction := "debit"
	switch o.Direction {
	case domain.DirectionBuy:
		settleAmount = money.Add(notional, commission)
		direction = "debit"
	case domain.DirectionSell:
		settleAmount = money.Sub(notional, commission)
		direction = "credit"
	}
	if !money.IsPositive(settleAmount) {
		return "", apperr.Validation("ukupan iznos posle provizije nije pozitivan")
	}
	return s.Settler.Settle(ctx, SettleInput{
		AccountID: o.AccountID,
		Direction: direction,
		Currency:  currency,
		Amount:    money.FormatAmount(settleAmount),
		OpID:      uuid.NewString(),
		IsActuary: o.UserKind == domain.KindEmployee,
		Purpose:   "Fill " + sec.Ticker,
	})
}

// settleForexLeg dispatches the spec p.42 paired settlement. base_amount
// = qty × contract_size; quote_amount = qty × contract_size × fillPrice.
// Direction maps order direction to the FX leg direction directly.
func (s *Service) settleForexLeg(
	ctx context.Context,
	o *domain.Order,
	sec *domain.Security,
	fillPrice string,
	qty int32,
	contractSize *big.Rat,
) (string, error) {
	if s.ForexSettler == nil {
		s.Log.Warn("forex settler not wired; forex fill skipped its cash leg",
			"order_id", o.ID, "ticker", sec.Ticker)
		return "", nil
	}
	if !sec.BaseCurrency.Supported() || !sec.QuoteCurrency.Supported() {
		return "", apperr.Internal("forex security missing base/quote currency", nil)
	}
	q := new(big.Rat).SetInt64(int64(qty))
	priceR, err := money.Parse(fillPrice)
	if err != nil {
		return "", apperr.Internal("fill price unparseable", err)
	}
	baseAmt := money.Mul(q, contractSize)
	quoteAmt := money.Mul(baseAmt, priceR)
	dir := "buy"
	if o.Direction == domain.DirectionSell {
		dir = "sell"
	}
	return s.ForexSettler.SettleForex(ctx, SettleForexInput{
		Direction:     dir,
		BaseCurrency:  sec.BaseCurrency,
		BaseAmount:    money.FormatAmount(baseAmt),
		QuoteCurrency: sec.QuoteCurrency,
		QuoteAmount:   money.FormatAmount(quoteAmt),
		OpID:          uuid.NewString(),
		Purpose:       "Forex fill " + sec.Ticker,
	})
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

// stopTriggered evaluates the stop condition per spec p.52 (STOP) and
// p.54 (STOP_LIMIT). Both order types compare ask for BUY and bid for
// SELL, but with different strictness:
//
//   STOP buy:        ask  >  stop   (strict)
//   STOP sell:       bid  <  stop   (strict)
//   STOP_LIMIT buy:  ask  >= stop   (loose — "dostigne ili pređe")
//   STOP_LIMIT sell: bid  <  stop   (strict — "padne ispod")
func (s *Service) stopTriggered(o *domain.Order, listing *domain.Listing) bool {
	stop, err := money.Parse(o.StopPrice)
	if err != nil {
		return false
	}
	var quoteStr string
	if o.Direction == domain.DirectionBuy {
		quoteStr = listing.Ask
	} else {
		quoteStr = listing.Bid
	}
	quote, err := money.Parse(quoteStr)
	if err != nil {
		return false
	}
	cmp := quote.Cmp(stop)
	switch o.OrderType {
	case domain.OrderStopLimit:
		if o.Direction == domain.DirectionBuy {
			return cmp >= 0
		}
		return cmp < 0
	default: // OrderStop
		if o.Direction == domain.DirectionBuy {
			return cmp > 0
		}
		return cmp < 0
	}
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

// limitFillPrice picks the fill price for a Limit (or triggered Stop-
// Limit) order per spec p.51. Buyer pays min(limit, ask); seller
// receives max(limit, bid). Falls back to the limit price when the
// listing's quote is unparseable.
func limitFillPrice(o *domain.Order, l *domain.Listing) string {
	limit, err := money.Parse(o.LimitPrice)
	if err != nil {
		return o.LimitPrice
	}
	switch o.Direction {
	case domain.DirectionBuy:
		ask, err := money.Parse(l.Ask)
		if err != nil {
			return o.LimitPrice
		}
		if ask.Cmp(limit) < 0 {
			return l.Ask
		}
		return o.LimitPrice
	case domain.DirectionSell:
		bid, err := money.Parse(l.Bid)
		if err != nil {
			return o.LimitPrice
		}
		if bid.Cmp(limit) > 0 {
			return l.Bid
		}
		return o.LimitPrice
	}
	return o.LimitPrice
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
//   maxIntervalSeconds = 1440 / (volume / remaining) = 1440 * remaining / volume
//   intervalSeconds    = Random(0, maxIntervalSeconds)
//   if afterHours: interval += 30 minutes (added AFTER the roll)
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
	maxInterval := cadenceMaxInterval(remaining, volume)

	interval := time.Duration(executionRand.Int63n(int64(maxInterval)))
	if o.AfterHours {
		// Spec p.56: "za ispunjavanje svakog dela Order-a se čeka
		// dodatnih 30 minuta" — the extra wait is added on top of the
		// rolled interval, not folded into the random distribution.
		interval += 30 * time.Minute
	}
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

// cadenceMaxInterval is the spec p.56 random-cap formula
// `1440 × remaining/volume` seconds. We scale to milliseconds so the
// integer division survives the typical case of volume ≫ remaining
// (e.g. remaining=1, volume=10000 → 144ms instead of truncating to 0).
func cadenceMaxInterval(remaining, volume int64) time.Duration {
	if volume <= 0 {
		volume = 1
	}
	if remaining <= 0 {
		return time.Millisecond
	}
	ms := 1440 * 1000 * remaining / volume
	if ms < 1 {
		ms = 1
	}
	return time.Duration(ms) * time.Millisecond
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
//   Market    : min(14% × order_total_notional, $7) total across all fills
//   Limit     : min(24% × order_total_notional, $12) total across all fills
//   Stop      : same as Market once triggered
//   StopLimit : same as Limit once triggered
//
// Spec p.55 reads "14% od približne cene celokupnog naloga ili $7,
// u zavisnosti od toga koji iznos je manji" — one cap on the whole
// order, not per partial fill. We compute the order-total commission
// once (using the create-time PricePerUnit snapshot as the
// "približna cena") and prorate it to this fill by qty share.
//
// The "$7" / "$12" caps are USD-denominated in the spec; we convert
// to the security's currency via the rate provider's ASK quote (no
// commission on the conversion). For USD securities this reduces to
// "7"/"12" directly; for an RSD security the cap becomes ~770 RSD.
//
// Actuary / supervisor employees trading on behalf of the bank pay
// the same trade commission as clients per spec p.55-56; only the
// FX leg is commission-free for them (handled bank-side).
func (s *Service) commissionFor(ctx context.Context, o *domain.Order, sec *domain.Security, fillNotional *big.Rat) (*big.Rat, error) {
	_ = fillNotional // kept in signature to avoid wider call-site churn
	var rateStr, capUSDStr string
	switch effectiveType(o) {
	case domain.OrderMarket:
		rateStr, capUSDStr = "0.14", "7"
	case domain.OrderLimit:
		rateStr, capUSDStr = "0.24", "12"
	default:
		rateStr, capUSDStr = "0.14", "7"
	}
	rate, _ := money.Parse(rateStr)

	// Order-total approximate notional = total_qty × contract_size × snapshot_price.
	cs, err := money.Parse(o.ContractSize)
	if err != nil || cs.Sign() == 0 {
		cs = money.MustParse("1")
	}
	priceSnap, err := money.Parse(o.PricePerUnit)
	if err != nil {
		return nil, apperr.Internal("price snapshot unparseable", err)
	}
	totalQty := new(big.Rat).SetInt64(int64(o.Quantity))
	totalNotional := money.Mul(money.Mul(totalQty, cs), priceSnap)
	totalPct := money.Mul(totalNotional, rate)

	cap, err := s.usdToSecurity(ctx, sec, capUSDStr)
	if err != nil {
		return nil, err
	}
	totalCommission := totalPct
	if totalCommission.Cmp(cap) > 0 {
		totalCommission = cap
	}

	// Prorate to this fill by qty share. Need the fill qty derived
	// from the notional and snapshot — we already have it implicitly
	// via the caller (fillNotional / (cs × snapshot_price)) but using
	// remaining-vs-total qty from the order is cleaner: this fill is
	// (qty_so_far_after_this − qty_so_far_before) / total_qty. The
	// caller will set fillQty via the helper below; this function
	// just returns the total-commission target. Caller (executeFill)
	// reads fillQty separately.

	// Default: full commission. The caller subtracts already-paid.
	return totalCommission, nil
}

// proratedCommission computes this fill's commission share, given the
// order-total commission and the fill's qty. Using a separate helper
// keeps commissionFor pure (no cumulative-state lookup) so the unit
// tests don't need a populated executions table.
//
//	share = totalCommission × (fillQty / totalQty)
func proratedCommission(totalCommission *big.Rat, fillQty, totalQty int32) *big.Rat {
	if totalQty <= 0 {
		return new(big.Rat)
	}
	frac := new(big.Rat).SetFrac64(int64(fillQty), int64(totalQty))
	return new(big.Rat).Mul(totalCommission, frac)
}

// usdToSecurity converts a USD-denominated reference amount (the
// spec's $7 / $12 commission cap) into the security's currency via
// the rate provider's ASK. Falls back to the raw string when the
// security is already USD or no rate provider is wired.
func (s *Service) usdToSecurity(ctx context.Context, sec *domain.Security, usdAmount string) (*big.Rat, error) {
	cap, err := money.Parse(usdAmount)
	if err != nil {
		return nil, apperr.Internal("usd cap unparseable", err)
	}
	cur := sec.Currency
	if cur == "" || cur == domain.CurrencyUSD {
		return cap, nil
	}
	if s.Rates == nil {
		// Dev stack without rates: keep the cap in USD-units. Same
		// pragmatic fallback the limit-math uses.
		return cap, nil
	}
	_, ask, err := s.Rates.Quote(ctx, domain.CurrencyUSD, cur)
	if err != nil {
		return nil, apperr.Internal("usd→security fx quote failed", err)
	}
	rate, err := money.Parse(ask)
	if err != nil {
		return nil, apperr.Internal("usd→security ask unparseable", err)
	}
	return money.Mul(cap, rate), nil
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
