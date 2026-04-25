package trading

import (
	"context"
	"errors"
	"log"
	"math"
	"math/rand"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// Order execution engine (#205, #206). Per spec pp. 51–56, 57–58 the engine
// fills approved orders in randomly sized chunks at randomly spaced
// intervals, with the interval scaled by the listing's daily volume. Covers
// all four order types: market fills every tick once scheduled; limit fills
// only when the quote is favourable against the user's limit (p.52); stop
// waits for its trigger (buy: ask ≥ stop, sell: bid ≤ stop) then behaves as
// market (p.53); stop_limit triggers identically and then behaves as limit
// (p.55). AON (p.56) commits the full remaining quantity in one chunk and
// relies on the settlement transaction to roll back when the placer/stub
// can't cover it — see chooseChunk for the heuristic.
//
// The implementation is a single ticker goroutine rather than per-order
// goroutines so cancellation/shutdown stays simple and state (next-fill
// time per order) lives in one map. In-memory state is intentional:
// restart re-rolls delays, which is acceptable at sim fidelity. Activation
// (triggered_at) is persisted so a restart doesn't re-arm a live stop.
const (
	executorTickInterval = 1 * time.Second
	// executorDefaultDelaySeconds is used when today's listing volume is
	// still 0 and the spec formula (24*60 * remaining / Volume) would
	// divide by zero. Picked short enough that the first fill happens
	// quickly, after which volume becomes non-zero and the formula takes
	// over on its own.
	executorDefaultDelaySeconds int64 = 30
	// afterHoursDelayBonus matches the spec's "add 30 min" rule on top of
	// the computed interval for orders flagged as after-hours (p.58).
	afterHoursDelayBonus = 30 * time.Minute
	// executorFailureBackoff keeps a persistently failing order from
	// hot-looping on the tick queue without dropping it permanently.
	executorFailureBackoff = 30 * time.Second
)

// StartExecutor launches the market-order execution worker. Mirrors the
// cancel-func pattern used by bank.StartScheduler so main.go treats both
// the same way.
func (s *Server) StartExecutor() func() {
	ctx, cancel := context.WithCancel(context.Background())
	go s.runExecutor(ctx)
	return cancel
}

func (s *Server) runExecutor(ctx context.Context) {
	nextFillAt := map[int64]time.Time{}
	ticker := time.NewTicker(executorTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.executorTick(now, nextFillAt)
		}
	}
}

// executorTick scans the approved order pool and fires any whose scheduled
// fill time has elapsed. Covers market, limit, stop and stop_limit orders —
// stop variants have a pre-trigger phase (checked every tick, not on the
// delay queue, so price movement is seen immediately) and a post-trigger
// fill phase that reuses the market/limit path. Listings only: options and
// forex have no ask/bid + listing_daily_price_info.volume, so they'd need
// their own engine and are out of scope here.
func (s *Server) executorTick(now time.Time, nextFillAt map[int64]time.Time) {
	var orders []Order
	err := s.db.Where(
		"status = ? AND is_done = ? AND listing_id IS NOT NULL",
		StatusApproved, false,
	).Find(&orders).Error
	if err != nil {
		log.Printf("[Executor] ERROR loading orders: %v", err)
		return
	}

	alive := make(map[int64]struct{}, len(orders))
	for _, o := range orders {
		alive[o.ID] = struct{}{}
	}
	for id := range nextFillAt {
		if _, ok := alive[id]; !ok {
			delete(nextFillAt, id)
		}
	}

	for i := range orders {
		o := &orders[i]

		// Stop / stop_limit activation is price-driven and runs every tick
		// regardless of the fill delay — the delay queue only governs fill
		// cadence, not trigger detection. Once armed, the order falls into
		// the delay-driven branch below on subsequent ticks.
		if needsActivation(o) {
			fired, err := s.checkActivation(o, now)
			if err != nil {
				log.Printf("[Executor] order %d activation check failed: %v", o.ID, err)
				continue
			}
			if !fired {
				continue
			}
			// Fire the first fill on the next tick so it goes through the
			// same locked-read path as everything else.
			nextFillAt[o.ID] = now
			continue
		}

		due, scheduled := nextFillAt[o.ID]
		if !scheduled {
			nextFillAt[o.ID] = now
			continue
		}
		if now.Before(due) {
			continue
		}
		next, err := s.executeFill(o, now)
		if err != nil {
			log.Printf("[Executor] order %d fill failed: %v", o.ID, err)
			nextFillAt[o.ID] = now.Add(executorFailureBackoff)
			continue
		}
		if next.IsZero() {
			delete(nextFillAt, o.ID)
			continue
		}
		nextFillAt[o.ID] = next
	}
}

// needsActivation reports whether the order is a stop-style order still
// waiting on its trigger. Plain market/limit orders always return false.
func needsActivation(o *Order) bool {
	if o.TriggeredAt != nil {
		return false
	}
	return o.OrderType == OrderStop || o.OrderType == OrderStopLimit
}

// checkActivation reads the current listing quote and, if the trigger
// condition is met, stamps triggered_at on the row. Spec (p.53, p.55):
// buy-side fires when ask ≥ stop, sell-side fires when bid ≤ stop.
func (s *Server) checkActivation(o *Order, now time.Time) (bool, error) {
	stop := stopTrigger(o)
	if stop <= 0 {
		return false, nil
	}
	var listing Listing
	if o.ListingID == nil {
		return false, nil
	}
	if err := s.db.First(&listing, *o.ListingID).Error; err != nil {
		return false, err
	}
	var triggered bool
	if o.Direction == DirectionBuy {
		triggered = listing.AskPrice > 0 && listing.AskPrice >= stop
	} else {
		triggered = listing.BidPrice > 0 && listing.BidPrice <= stop
	}
	if !triggered {
		return false, nil
	}
	// Persist triggered_at so a restart doesn't re-arm the order. Memory
	// copy updated too so the caller can tell activation fired.
	res := s.db.Model(&Order{}).
		Where("id = ? AND triggered_at IS NULL", o.ID).
		Update("triggered_at", now)
	if res.Error != nil {
		return false, res.Error
	}
	t := now
	o.TriggeredAt = &t
	return true, nil
}

// stopTrigger returns the activation threshold for a stop-style order. Plain
// stop orders keep it in price_per_unit; stop_limit orders keep it in
// stop_price (price_per_unit is their limit).
func stopTrigger(o *Order) int64 {
	switch o.OrderType {
	case OrderStop:
		return o.PricePerUnit
	case OrderStopLimit:
		return o.StopPrice
	}
	return 0
}

// executeFill fills one chunk in a transaction. Returns the scheduled time
// for the next chunk; the zero time signals "order is complete, drop it".
func (s *Server) executeFill(o *Order, now time.Time) (time.Time, error) {
	var next time.Time
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Re-lock + re-check: a concurrent cancel or prior fill on the same
		// order might have moved the row out from under this tick.
		locked, err := lockOrder(tx, o.ID)
		if err != nil {
			return err
		}
		if locked.Status != StatusApproved || locked.IsDone || locked.RemainingPortions <= 0 {
			next = time.Time{}
			return nil
		}
		if locked.ListingID == nil {
			next = time.Time{}
			return nil
		}
		// Stop / stop_limit orders only reach here post-activation. If an
		// activation race left triggered_at unset, defer to the next tick —
		// checkActivation will re-evaluate and arm the row.
		if needsActivation(locked) {
			next = now.Add(executorTickInterval)
			return nil
		}

		var listing Listing
		if err := tx.First(&listing, *locked.ListingID).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		var exch Exchange
		if err := tx.First(&exch, listing.ExchangeID).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}

		// Clock check: the delay may have fired while the exchange is
		// closed (e.g. order was scheduled late in the session and slept
		// past close). Postpone one tick rather than filling against a
		// stale quote.
		if !IsOpen(exch, now) {
			next = now.Add(executorTickInterval)
			return nil
		}

		fillPPU, ok := fillPriceForOrder(locked, listing)
		if !ok {
			// Limit/stop_limit: ask/bid moved away from the user's limit.
			// Skip this fill and let the standard delay schedule retry.
			next = now.Add(executorTickInterval)
			return nil
		}
		chunk := chooseChunk(locked)
		chunkCostInstr := chunk * locked.ContractSize * fillPPU

		acc, err := s.bank.GetAccountByNumber(locked.AccountNumber)
		if err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}

		instrCurrency := exch.Currency
		settle, err := s.planSettlement(acc.Currency, instrCurrency, chunkCostInstr, locked.Direction)
		if err != nil {
			return err
		}

		if err := applySettlement(tx, locked.Direction, locked.AccountNumber, acc.Currency, instrCurrency, settle); err != nil {
			return err
		}

		// Holding bookkeeping (#207): buy-fills upsert into the placer's
		// position with a quantity-weighted avg_cost; sell-fills deduct and
		// fail (FailedPrecondition → tick-level backoff) when the seller is
		// short. The check happens after applySettlement so a refused sell
		// rolls the money movement back too — the deferred error path the
		// gorm Transaction wrapper already provides.
		assetCol, assetID, err := holdingAssetKey(tx, locked)
		if err != nil {
			return err
		}
		var soldFromSnapshot *Holding
		if locked.Direction == DirectionBuy {
			accountID, err := holdingAccountID(tx, locked.AccountNumber)
			if err != nil {
				return err
			}
			perContractAccCost := settle.accAmount / chunk
			if err := upsertHoldingOnBuy(tx, locked.PlacerID, accountID, assetCol, assetID, chunk, perContractAccCost, now); err != nil {
				return err
			}
		} else {
			soldFromSnapshot, err = deductHoldingOnSell(tx, locked.PlacerID, assetCol, assetID, chunk, now)
			if err != nil {
				return err
			}
		}

		fill := OrderFill{
			OrderID:      locked.ID,
			Portions:     chunk,
			PricePerUnit: fillPPU,
			FxRate:       settle.fxRate,
		}
		if err := tx.Create(&fill).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}

		// Capital-gains tax (#208): stock sell-fills with positive dobit owe
		// 15% (RSD). recordCapitalGain is a no-op on non-stock snapshots and
		// loss fills, so it's safe to call unconditionally on the sell path.
		// RSD accounts skip the rate lookup — the conversion is the identity.
		if locked.Direction == DirectionSell && soldFromSnapshot != nil {
			rateAccRSD := 1.0
			if acc.Currency != "RSD" {
				r, err := s.bank.GetExchangeRateToRSD(acc.Currency)
				if err != nil {
					return status.Errorf(codes.Internal, "%v", err)
				}
				rateAccRSD = r
			}
			if err := recordCapitalGain(tx, soldFromSnapshot, fill.ID, chunk, settle.accAmount, rateAccRSD, now); err != nil {
				return err
			}
		}

		newVolume, err := upsertDailyVolume(tx, *locked.ListingID, now, chunk, listing)
		if err != nil {
			return err
		}

		newRemaining := locked.RemainingPortions - chunk
		updates := map[string]any{
			"remaining_portions": newRemaining,
			"last_modification":  now,
		}
		if newRemaining <= 0 {
			updates["is_done"] = true
			updates["status"] = string(StatusDone)
		}
		if err := tx.Model(locked).Updates(updates).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}

		if newRemaining <= 0 {
			next = time.Time{}
			return nil
		}
		next = now.Add(nextDelay(newRemaining, newVolume, locked.AfterHours))
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	return next, nil
}

// fillPricePerUnit returns the per-unit price the chunk should fill at:
// ask for a buy (placer pays the asking price), bid for a sell (placer gets
// the bid). Multiplied by contract_size downstream for the chunk total.
func fillPricePerUnit(d OrderDirection, l Listing) int64 {
	if d == DirectionBuy {
		return l.AskPrice
	}
	return l.BidPrice
}

// fillPriceForOrder resolves the per-unit fill price for any order type.
// Market and triggered stop orders take the ask/bid directly. Limit and
// triggered stop_limit orders fill only when the quote is favourable against
// the user's limit (spec p.52): buy fills at min(limit, ask) when ask ≤
// limit; sell fills at max(limit, bid) when bid ≥ limit. ok=false means the
// quote is out of range and the executor should skip the tick.
func fillPriceForOrder(o *Order, l Listing) (int64, bool) {
	switch o.OrderType {
	case OrderMarket, OrderStop:
		ppu := fillPricePerUnit(o.Direction, l)
		return ppu, ppu > 0
	case OrderLimit, OrderStopLimit:
		limit := o.PricePerUnit
		if o.Direction == DirectionBuy {
			if l.AskPrice <= 0 || l.AskPrice > limit {
				return 0, false
			}
			if limit < l.AskPrice {
				return limit, true
			}
			return l.AskPrice, true
		}
		if l.BidPrice <= 0 || l.BidPrice < limit {
			return 0, false
		}
		if limit > l.BidPrice {
			return limit, true
		}
		return l.BidPrice, true
	}
	return 0, false
}

// chooseChunk picks how many portions to fill this tick. AON orders must
// fill in a single shot (spec p.56): we commit to the full remaining
// quantity and let the settlement transaction decide whether the notional
// liquidity is there — a failed debit rolls the whole chunk back and the
// executor retries next tick. That's our proxy for "notional liquidity
// allows": without a real orderbook we can't pre-probe depth, so we use a
// commit-and-rollback probe, which for this sim maps to the placer's (buy)
// or bank-stub's (sell) balance at the fill currency.
func chooseChunk(o *Order) int64 {
	if o.AllOrNone {
		return o.RemainingPortions
	}
	return randomChunk(o.RemainingPortions)
}

// randomChunk picks the portion count for this fill: Random(1, remaining).
// Always at least 1 so progress is guaranteed when called on an order with
// work left.
func randomChunk(remaining int64) int64 {
	if remaining <= 1 {
		return remaining
	}
	return rand.Int63n(remaining) + 1
}

// nextDelay implements the spec formula: Random(0, 24*60 / (Volume /
// remaining)) seconds, with an after-hours bonus of 30 min. Volume==0 is a
// first-of-day edge case (no daily row yet) — we fall back to a short
// default so the first fill lands promptly and builds volume for later
// ticks to key off.
func nextDelay(remaining, volume int64, afterHours bool) time.Duration {
	var maxSec int64
	if volume <= 0 || remaining <= 0 {
		maxSec = executorDefaultDelaySeconds
	} else {
		maxSec = 1440 * remaining / volume
		if maxSec <= 0 {
			maxSec = 1
		}
	}
	d := time.Duration(rand.Int63n(maxSec+1)) * time.Second
	if afterHours {
		d += afterHoursDelayBonus
	}
	return d
}

// settlement captures a single chunk's money movement across currencies.
// feeInstrument is the chunk cost in the instrument's currency; accAmount
// is its equivalent in the placer account's currency (equal to
// feeInstrument for same-currency orders). fxRate is nil for same-currency
// and otherwise snapshots rateInstrRSD/rateAccRSD for audit.
type settlement struct {
	accAmount   int64
	feeInstr    int64
	fxRate      *float64
	direction   OrderDirection
	accCurrency string
	instrCurr   string
}

// planSettlement converts the instrument-currency chunk cost into the
// account's currency via the existing menjacnica rates-to-RSD service
// (bank.GetExchangeRateToRSD). Rounding sides with the bank: buyers round
// up, sellers round down, so the placer doesn't pocket a rounding penny on
// either side. No menjacnica fee is charged at fill — the placement
// commission already covered it (commission.go, spec pp.27, 57).
func (s *Server) planSettlement(accCurrency, instrCurrency string, chunkCostInstr int64, dir OrderDirection) (settlement, error) {
	out := settlement{
		feeInstr:    chunkCostInstr,
		direction:   dir,
		accCurrency: accCurrency,
		instrCurr:   instrCurrency,
	}
	if accCurrency == instrCurrency {
		out.accAmount = chunkCostInstr
		return out, nil
	}
	rateAccRSD, err := s.bank.GetExchangeRateToRSD(accCurrency)
	if err != nil {
		return settlement{}, status.Errorf(codes.Internal, "%v", err)
	}
	rateInstrRSD, err := s.bank.GetExchangeRateToRSD(instrCurrency)
	if err != nil {
		return settlement{}, status.Errorf(codes.Internal, "%v", err)
	}
	rate := rateInstrRSD / rateAccRSD
	if dir == DirectionBuy {
		out.accAmount = int64(math.Ceil(float64(chunkCostInstr) * rate))
	} else {
		out.accAmount = int64(math.Floor(float64(chunkCostInstr) * rate))
	}
	out.fxRate = &rate
	return out, nil
}

// applySettlement moves the money for one chunk. With no real orderbook we
// use the bank-system client (system@banka3.rs) as the standing
// counterparty: buys debit the placer and credit the system's
// instrument-currency account; sells do the reverse. The same per-currency
// system accounts back commission.go's fee pool, so we don't seed a
// dedicated trading-stub account.
func applySettlement(tx *gorm.DB, dir OrderDirection, account, accCurrency, instrCurrency string, s settlement) error {
	if dir == DirectionBuy {
		if err := debitPlacer(tx, account, s.accAmount); err != nil {
			return err
		}
		return creditFeeAccount(tx, instrCurrency, s.feeInstr)
	}
	// Sell: stub pays the placer. Debit the system account first so we
	// surface an insufficient-funds failure before mutating the placer.
	if err := debitSystemAccount(tx, instrCurrency, s.feeInstr); err != nil {
		return err
	}
	return creditPlacer(tx, account, s.accAmount)
}

func debitPlacer(tx *gorm.DB, account string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	res := tx.Exec(
		`UPDATE accounts SET balance = balance - ? WHERE number = ? AND balance >= ?`,
		amount, account, amount,
	)
	if res.Error != nil {
		return status.Errorf(codes.Internal, "%v", res.Error)
	}
	if res.RowsAffected == 0 {
		return status.Error(codes.FailedPrecondition, "insufficient funds for fill")
	}
	return nil
}

func creditPlacer(tx *gorm.DB, account string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	res := tx.Exec(
		`UPDATE accounts SET balance = balance + ? WHERE number = ?`,
		amount, account,
	)
	if res.Error != nil {
		return status.Errorf(codes.Internal, "%v", res.Error)
	}
	if res.RowsAffected == 0 {
		return status.Error(codes.FailedPrecondition, "placer account missing")
	}
	return nil
}

// debitSystemAccount mirrors creditFeeAccount but in the opposite
// direction: used by sell-side fills to pull funds out of the bank-stub
// counterparty. Guards on balance so an under-funded stub surfaces as
// FailedPrecondition rather than silently going negative.
func debitSystemAccount(tx *gorm.DB, currency string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	var feeAccount string
	err := tx.Raw(
		`SELECT a.number FROM accounts a
		 JOIN clients c ON c.id = a.owner
		 WHERE c.email = ? AND a.currency = ?
		 LIMIT 1`,
		bankSystemOwnerEmail, currency,
	).Scan(&feeAccount).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return status.Errorf(codes.FailedPrecondition, "no bank stub account for %s", currency)
		}
		return status.Errorf(codes.Internal, "%v", err)
	}
	if feeAccount == "" {
		return status.Errorf(codes.FailedPrecondition, "no bank stub account for %s", currency)
	}
	res := tx.Exec(
		`UPDATE accounts SET balance = balance - ? WHERE number = ? AND balance >= ?`,
		amount, feeAccount, amount,
	)
	if res.Error != nil {
		return status.Errorf(codes.Internal, "%v", res.Error)
	}
	if res.RowsAffected == 0 {
		return status.Errorf(codes.FailedPrecondition, "bank stub account underfunded for %s", currency)
	}
	return nil
}

// upsertDailyVolume bumps today's listing_daily_price_info.volume by the
// fill size, creating today's row from the listing's current quote if it
// doesn't exist yet. Returns the post-update volume so the executor can
// feed it into the delay formula without an extra SELECT.
func upsertDailyVolume(tx *gorm.DB, listingID int64, now time.Time, chunk int64, listing Listing) (int64, error) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var volume int64
	err := tx.Raw(
		`INSERT INTO listing_daily_price_info (listing_id, date, price, ask_price, bid_price, change, volume)
		 VALUES (?, ?, ?, ?, ?, 0, ?)
		 ON CONFLICT (listing_id, date)
		 DO UPDATE SET volume = listing_daily_price_info.volume + EXCLUDED.volume
		 RETURNING volume`,
		listingID, today, listing.Price, listing.AskPrice, listing.BidPrice, chunk,
	).Scan(&volume).Error
	if err != nil {
		return 0, status.Errorf(codes.Internal, "%v", err)
	}
	return volume, nil
}
