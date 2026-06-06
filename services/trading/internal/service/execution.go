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
	"log/slog"
	"math/big"
	"math/rand"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/bizmetric"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// maxResumeAttempts caps how many transient retries the recovery sweep
// burns on a single pending row before the log level escalates from
// WARN to ERROR. The sweep keeps trying past this — bank-side commits
// can take arbitrarily long to converge with the trading-side booking
// when the database flaps — but operators need a loud signal that a
// row is stuck. Permanent bank errors short-circuit this entirely:
// they abandon on the first attempt (see isPermanentBankError).
const maxResumeAttempts = 8

// isPermanentBankError reports whether a bank settle error reflects an
// input the trading service got wrong in a way time won't fix. Examples:
// the order's source account is the wrong kind (`system` vs
// `forex_book`), the security's settlement date is in the past, the
// account's currency doesn't match. Retrying these every tick produces
// a never-ending log loop and pins worker capacity on a row that can
// never make progress.
//
// Transient codes (Unavailable, DeadlineExceeded, Internal, Aborted) and
// any non-status error (book-keeping failures, DB blips) are left to
// retry. Bank.SettleTrade is idempotent on (op_id, leg_index) so
// retrying is always safe — and book-keeping failures specifically are
// where bank-committed-but-not-booked rows live, which we MUST resolve
// by retrying (never abandon).
func isPermanentBankError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.InvalidArgument,
		codes.FailedPrecondition,
		codes.PermissionDenied,
		codes.NotFound,
		codes.OutOfRange,
		codes.Unauthenticated:
		return true
	}
	return false
}

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
	Fired        bool
	Order        *domain.Order
	Execution    *domain.OrderExecution
	NextEarliest time.Time
}

// ProcessOrderTick is invoked by the execution worker for one active
// order. Returns whether a fill fired, and (when none fires) the
// earliest time the worker should retry this order.
//
// Recovery first: if a pending-execution row exists for this order, we
// resume that fill (re-call bank with the same op_id, then book) and
// return without rolling cadence for a fresh fill. The pending row
// persists across worker crashes; bank's idempotency on op_id makes
// the re-call safe regardless of whether the previous attempt's bank
// settle committed or not.
//
// Fresh-fill path is gated on:
//   - cancelled / done flags (worker shouldn't pick these but defend);
//   - STOP / STOP_LIMIT trigger condition vs current price;
//   - LIMIT price condition vs current ask (buy) / bid (sell);
//   - cadence based on spec p.56 random-interval formula.
func (s *Service) ProcessOrderTick(ctx context.Context, o *domain.Order) (processFillResult, error) {
	res := processFillResult{Order: o}

	// Resume a pending fill before anything else. We do this regardless
	// of cancelled/done state — once a pending row exists, bank may
	// already have settled and we owe the booking.
	pending, err := s.Store.GetPendingExecutionForOrder(ctx, o.ID)
	if err != nil {
		return res, err
	}
	if pending != nil {
		exec, resumeErr := s.resumePendingFill(ctx, o, pending)
		if resumeErr == nil {
			res.Fired = true
			res.Execution = exec
			return res, nil
		}

		// Resume failed. Two regimes:
		//
		//   1. Bank rejected the call with a permanent code (invalid
		//      account kind, currency mismatch, settlement in the past
		//      …). Retrying every tick can't help. Mark the row
		//      abandoned and cancel the parent order so the fresh-fill
		//      sweep doesn't immediately seed a new pending row with
		//      the same bad inputs.
		//
		//   2. Anything else — transient bank failure, DB blip,
		//      book-keeping error after bank committed. Bump the
		//      attempts counter, escalate log level past a threshold,
		//      and keep trying. Bank.SettleTrade is idempotent on
		//      (op_id, leg_index) so retries are always safe; book-
		//      keeping is the only operation that converges a
		//      bank-committed-but-not-booked row.
		errMsg := resumeErr.Error()
		if isPermanentBankError(resumeErr) {
			if abErr := s.abandonPendingFill(ctx, o, pending, errMsg); abErr != nil {
				s.Log.Error("recovery: failed to abandon pending exec",
					"order_id", o.ID, "exec_id", pending.ID,
					"underlying", errMsg, "err", abErr.Error())
				return res, resumeErr
			}
			s.Log.Warn("recovery: pending execution abandoned (permanent bank error)",
				"order_id", o.ID, "exec_id", pending.ID, "err", errMsg)
			return res, nil
		}
		attempts, recErr := s.Store.RecordResumeFailure(ctx, pending.ID, errMsg)
		if recErr != nil {
			s.Log.Warn("recovery: failed to bump attempts counter",
				"order_id", o.ID, "exec_id", pending.ID, "err", recErr.Error())
			return res, resumeErr
		}
		level := slog.LevelWarn
		if attempts >= maxResumeAttempts {
			level = slog.LevelError
		}
		s.Log.Log(ctx, level, "recovery: pending execution retry",
			"order_id", o.ID, "exec_id", pending.ID,
			"attempts", attempts, "err", errMsg)
		return res, resumeErr
	}

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

	// Spec p.39 — no fills while the exchange is fully closed (either
	// scheduled outside hours and outside the spec p.56 after-hours
	// window, or admin-forced closed via the override toggle). Without
	// this the cadence sweep settles money + moves shares regardless of
	// market state, which the org-file audit caught: "hartija se
	// deductuje nezavisno od stanja berze". Runs AFTER the resume-pending
	// path so bank-side reconciliation of an already-settled fill still
	// converges even with the market closed.
	if listing.ExchangeMIC != "" {
		ex, exErr := s.Store.GetExchange(ctx, listing.ExchangeMIC)
		if exErr == nil {
			st := s.resolveMarketState(ex, s.now())
			if !st.IsOpen && !st.IsAfterHours {
				res.NextEarliest = s.now().Add(s.tickRetryInterval())
				return res, nil
			}
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

// executeFill drives a fresh partial-fill through the saga:
//
//  1. Insert a pending order_executions row in its own tx — its UUID
//     is the deterministic op_id for the bank settle.
//  2. Bank settles the cash leg, idempotent on op_id (bank migration
//     0011's unique (op_id, leg_index) makes retries safe).
//  3. In one tx: mark the row settled, advance order progress, apply
//     the portfolio change, and write realized_gain on a sell.
//
// A worker crash anywhere between (1) and (3) leaves a pending row.
// resumePendingFill picks it up on the next tick: bank.Settle is
// idempotent so re-calling with the same op_id returns the existing
// legs; the booking tx is the only operation needed to converge.
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
	// Spec p.42: forex pairs settle paired-currency without a commission
	// fee — the bank's profit comes off the rate. Skip the commission
	// math entirely so a future refactor can't accidentally re-enable it.
	var commission *big.Rat
	if sec.Type == domain.SecurityForex {
		commission = new(big.Rat)
	} else {
		totalCommission, err := s.commissionFor(ctx, o, sec)
		if err != nil {
			return nil, err
		}
		commission = proratedCommission(totalCommission, qty, o.Quantity)
	}

	// (1) Pre-write the pending row in its own tx so its UUID survives a
	// crash in the bank call. Subsequent ticks find it and resume.
	var pending *domain.OrderExecution
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		row, err := s.Store.InsertPendingExecution(ctx, tx, &domain.OrderExecution{
			OrderID:       o.ID,
			Quantity:      qty,
			PricePerUnit:  fillPrice,
			TotalAmount:   money.FormatAmount(notional),
			CommissionAmt: money.FormatAmount(commission),
		})
		if err != nil {
			return err
		}
		pending = row
		return nil
	}); err != nil {
		return nil, err
	}

	return s.completeFill(ctx, o, sec, pending, contractSize)
}

// resumePendingFill re-drives the saga for an order whose previous fill
// attempt left a pending row. We rebuild the in-memory state from the
// row's persisted amounts (the original qty / price / commission) and
// re-call the bank with the same op_id; bank-side idempotency makes
// the re-call a no-op when the previous attempt already committed.
func (s *Service) resumePendingFill(ctx context.Context, o *domain.Order, pending *domain.OrderExecution) (*domain.OrderExecution, error) {
	if s.Settler == nil {
		return nil, apperr.Internal("trade settler not wired", nil)
	}
	sec, err := s.Store.GetSecurity(ctx, o.SecurityID)
	if err != nil {
		return nil, err
	}
	contractSize := o.ContractSize
	if contractSize == "" {
		contractSize = "1"
	}
	s.Log.Info("resuming pending execution",
		"order_id", o.ID, "exec_id", pending.ID, "qty", pending.Quantity)
	return s.completeFill(ctx, o, sec, pending, contractSize)
}

// abandonPendingFill is the terminal cleanup for a pending row whose
// resume failed with a permanent bank error. Marks the row 'abandoned'
// and cancels the parent order in one tx so the next tick's recovery
// sweep won't re-pick it (status='pending' filter) and the fresh-fill
// sweep won't start a replacement (gated on `o.Cancelled`).
//
// Cancelling an already-cancelled order is a no-op (CancelOrderTx
// tolerates it) so this is safe for orders the supervisor cancelled
// before the sweep got here.
func (s *Service) abandonPendingFill(ctx context.Context, o *domain.Order, pending *domain.OrderExecution, errMsg string) error {
	return s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		if err := s.Store.MarkPendingAbandoned(ctx, tx, pending.ID, errMsg); err != nil {
			return err
		}
		return s.Store.CancelOrderTx(ctx, tx, o.ID)
	})
}

// completeFill is the shared steps (2) + (3) of the saga. Both fresh
// fills (after InsertPendingExecution) and resumes call into here.
func (s *Service) completeFill(
	ctx context.Context,
	o *domain.Order,
	sec *domain.Security,
	pending *domain.OrderExecution,
	contractSize string,
) (*domain.OrderExecution, error) {
	notional, err := money.Parse(pending.TotalAmount)
	if err != nil {
		return nil, apperr.Internal("pending notional unparseable", err)
	}
	commission, err := money.Parse(pending.CommissionAmt)
	if err != nil {
		return nil, apperr.Internal("pending commission unparseable", err)
	}

	// (2) Bank settle, deterministic op_id = pending row's UUID. Both
	// SettleTrade and SettleForexFill are idempotent on op_id (bank
	// migration 0011), so a retry after a partial failure is a no-op.
	settledOpID, err := s.settleCashLeg(ctx, o, sec, pending.PricePerUnit, pending.Quantity, notional, commission, pending.ID)
	if err != nil {
		return nil, err
	}

	// (3) Mark settled + advance + book in one tx. AdvanceOrderProgress
	// no longer gates on `cancelled = false` — once we have a pending
	// row, the cancel must not strand bank-settled money.
	var exec *domain.OrderExecution
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		if err := s.Store.MarkExecutionSettled(ctx, tx, pending.ID, settledOpID); err != nil {
			return err
		}
		if _, err := s.Store.AdvanceOrderProgress(ctx, tx, o.ID, pending.Quantity); err != nil {
			return err
		}

		// Spec p.42: forex pairs are not held — buying a pair means
		// selling one currency and buying the other, with no portfolio
		// position. Skip portfolio + realized-gain bookkeeping for forex.
		if sec.Type == domain.SecurityForex {
			return nil
		}

		switch o.Direction {
		case domain.DirectionBuy:
			if _, err := s.Store.ApplyBuyFill(ctx, tx,
				o.UserID, string(o.UserKind), o.SecurityID, o.AccountID,
				pending.Quantity, pending.PricePerUnit,
			); err != nil {
				return err
			}
		case domain.DirectionSell:
			avgPrice, _, err := s.Store.ApplySellFill(ctx, tx,
				o.UserID, string(o.UserKind), o.SecurityID, o.AccountID, pending.Quantity,
			)
			if err != nil {
				return err
			}
			// Spec p.71-76: fund-actor sells are pre-tax; the tax bites
			// the client at withdrawal time. Skip the realized_gain
			// insert here so the monthly tax cron doesn't
			// double-tax the fund.
			if o.UserKind == domain.KindFund {
				break
			}
			if err := s.recordRealizedGain(ctx, tx, o, sec, pending.Quantity, pending.PricePerUnit, avgPrice, contractSize); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.Log.ErrorContext(ctx, "fill book-keeping failed after bank settle",
			"order_id", o.ID, "exec_id", pending.ID, "op_id", settledOpID, "err", err.Error())
		bizmetric.TradeCompleted(ctx, string(o.Direction), string(sec.Type), "settle_failed")
		return nil, err
	}
	pending.BankOpID = settledOpID
	pending.Status = "settled"
	exec = pending
	bizmetric.TradeCompleted(ctx, string(o.Direction), string(sec.Type), "ok")
	return exec, nil
}

// settleCashLeg routes the cash leg of a fill to the right bank
// primitive. Stocks/futures/options use SettleTrade against the user
// account; forex uses SettleForexFill against the bank's per-currency
// forex_book accounts (spec p.42). The op_id is the pending execution
// row's UUID — deterministic across retries and recoveries.
func (s *Service) settleCashLeg(
	ctx context.Context,
	o *domain.Order,
	sec *domain.Security,
	fillPrice string,
	qty int32,
	notional, commission *big.Rat,
	opID string,
) (string, error) {
	if sec.Type == domain.SecurityForex {
		return s.settleForexLeg(ctx, o, sec, fillPrice, qty, opID)
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
		OpID:      opID,
		IsActuary: o.IsActuary,
		Purpose:   "Fill " + sec.Ticker,
	})
}

// settleForexLeg dispatches the spec p.42 paired settlement. base_amount
// = qty × contract_size; quote_amount = qty × contract_size × fillPrice.
// Direction maps order direction to the FX leg direction directly.
// op_id is the pending row's UUID for deterministic retries.
func (s *Service) settleForexLeg(
	ctx context.Context,
	o *domain.Order,
	sec *domain.Security,
	fillPrice string,
	qty int32,
	opID string,
) (string, error) {
	if s.ForexSettler == nil {
		s.Log.Warn("forex settler not wired; forex fill skipped its cash leg",
			"order_id", o.ID, "ticker", sec.Ticker)
		return "", nil
	}
	if !sec.BaseCurrency.Supported() || !sec.QuoteCurrency.Supported() {
		return "", apperr.Internal("forex security missing base/quote currency", nil)
	}
	cs, err := money.Parse(o.ContractSize)
	if err != nil || cs.Sign() == 0 {
		cs = money.MustParse("1")
	}
	q := new(big.Rat).SetInt64(int64(qty))
	priceR, err := money.Parse(fillPrice)
	if err != nil {
		return "", apperr.Internal("fill price unparseable", err)
	}
	baseAmt := money.Mul(q, cs)
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
		OpID:          opID,
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
//	STOP buy:        ask  >  stop   (strict)
//	STOP sell:       bid  <  stop   (strict)
//	STOP_LIMIT buy:  ask  >= stop   (loose — "dostigne ili pređe")
//	STOP_LIMIT sell: bid  <  stop   (strict — "padne ispod")
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
//	maxIntervalSeconds = 1440 / (volume / remaining) = 1440 * remaining / volume
//	intervalSeconds    = Random(0, maxIntervalSeconds)
//	if afterHours: interval += 30 minutes (added AFTER the roll)
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
	interval := rolledFillInterval(maxInterval, o.AfterHours)
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

// rolledFillInterval rolls a per-tick cadence interval. Spec p.56:
// the random component is uniform on [0, maxInterval); after-hours
// orders add a flat 30 min on top of the roll, NOT folded into the
// uniform distribution. Extracted so the +30min branch is unit-
// testable without driving cadenceReady's store-side timing.
func rolledFillInterval(maxInterval time.Duration, afterHours bool) time.Duration {
	interval := time.Duration(executionRand.Int63n(int64(maxInterval)))
	if afterHours {
		interval += 30 * time.Minute
	}
	return interval
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
	t, ok, err := s.Store.LatestExecutionAt(ctx, o.ID)
	if err != nil {
		return 0, false, err
	}
	if !ok {
		return 0, false, nil
	}
	return s.now().Sub(t), true, nil
}

// commissionFor returns the per-fill commission per spec p.55-56.
//
//	Market    : min(14% × order_total_notional, $7) total across all fills
//	Limit     : min(24% × order_total_notional, $12) total across all fills
//	Stop      : same as Market once triggered
//	StopLimit : same as Limit once triggered
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
func (s *Service) commissionFor(ctx context.Context, o *domain.Order, sec *domain.Security) (*big.Rat, error) {
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

	// Returns the order-total commission target. Caller (executeFill)
	// prorates per fill via proratedCommission(fillQty, totalQty).
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
// spec's $7 / $12 commission cap) into the security's currency. The
// exchange catalog only holds X→RSD rows, so cross-currency routes
// through RSD: USD→RSD at ASK_USD, then RSD→cur by dividing by
// ASK_cur. Falls back to the raw string when the security is already
// USD or no rate provider is wired.
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
	_, askUSD, err := s.Rates.Quote(ctx, domain.CurrencyUSD, domain.CurrencyRSD)
	if err != nil {
		return nil, apperr.Internal("usd→rsd fx quote failed", err)
	}
	askUSDR, err := money.Parse(askUSD)
	if err != nil {
		return nil, apperr.Internal("usd→rsd ask unparseable", err)
	}
	rsdCap := money.Mul(cap, askUSDR)
	if cur == domain.CurrencyRSD {
		return rsdCap, nil
	}
	_, askCur, err := s.Rates.Quote(ctx, cur, domain.CurrencyRSD)
	if err != nil {
		return nil, apperr.Internal("security→rsd fx quote failed", err)
	}
	askCurR, err := money.Parse(askCur)
	if err != nil {
		return nil, apperr.Internal("security→rsd ask unparseable", err)
	}
	out, derr := money.Div(rsdCap, askCurR)
	if derr != nil {
		return nil, apperr.Internal("usd→security divide failed", derr)
	}
	return out, nil
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
//
// Two passes: first the recovery sweep over orders with a pending
// execution row (these may be cancelled or otherwise excluded from the
// active set, but they still own a fill we must finish booking); then
// the regular active-order sweep. We dedupe so an order with a pending
// row + still-active status isn't ticked twice.
func (s *Service) RunExecutionTick(ctx context.Context) (int, error) {
	pendingIDs, err := s.Store.ListOrderIDsWithPendingExecutions(ctx, 200)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{}, len(pendingIDs))
	fired := 0
	for _, id := range pendingIDs {
		seen[id] = struct{}{}
		o, err := s.Store.GetOrder(ctx, id)
		if err != nil {
			s.Log.Warn("recovery: load order failed", "order_id", id, "err", err.Error())
			continue
		}
		res, err := s.ProcessOrderTick(ctx, o)
		if err != nil {
			s.Log.Warn("recovery: order tick failed", "order_id", id, "err", err.Error())
			continue
		}
		if res.Fired {
			fired++
		}
	}

	orders, err := s.Store.GetActiveOrdersForExecution(ctx, 200)
	if err != nil {
		return fired, err
	}
	for _, o := range orders {
		if _, dup := seen[o.ID]; dup {
			continue
		}
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
