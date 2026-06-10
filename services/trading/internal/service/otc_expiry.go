// OTC contract expiry sweep. Spec p.69: a contract
// whose settlement_date has passed without exercise flips to `expired`.
//
// What happens
// ============
//   * The contract row's status changes from `active` → `expired`.
//   * The seller's holding `reserved_count` decrements by the
//     contract's quantity (the reservation locked at offer-accept time
//     is released — the underlying shares are free to trade again).
//   * The premium is **NOT** refunded (spec p.69 — the
//     premium is the buyer's sunk cost; that's the entire reason an
//     option carries a non-zero premium). No bank-side write happens
//     in this path.
//   * A best-effort notification fires to the buyer ("vaš ugovor je
//     istekao bez izvršenja"); errors don't block the sweep.
//
// The expiry sweep is intentionally NOT a SAGA: there's no
// cross-service write (no bank call), so the orchestration overhead
// would buy nothing. It's a plain trading-side tx per contract.

package service

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

// SweepExpiredOTCContractsResult bundles cron telemetry.
type SweepExpiredOTCContractsResult struct {
	ContractsExpired int
	SharesReleased   int32
	// ContractsWarned is the count of pre-expiry warnings sent (S63).
	ContractsWarned int
	// OffersExpired is the count of open offers auto-expired after
	// 3 business days of inactivity (todoSpec C4).
	OffersExpired int
	// OffersWarned is the count of pre-expiry warnings sent for offers
	// approaching the inactivity cutoff.
	OffersWarned int
}

// expiryWarnDays is the number of calendar days before settlement_date
// at which the holder receives a single pre-expiry warning (scenario S63).
const expiryWarnDays = 3

// daysUntilExpiry returns the number of whole calendar days from today
// (in loc) until the contract's settlement_date calendar day (also in loc).
// A contract expiring later today returns 0; one expiring tomorrow returns 1.
func daysUntilExpiry(now time.Time, settlementDate time.Time, loc *time.Location) int {
	todayDate := truncToDay(now, loc)
	expiryDate := truncToDay(settlementDate, loc)
	return int(expiryDate.Sub(todayDate) / (24 * 60 * 60 * 1e9))
}

// truncToDay truncates t to midnight in loc.
func truncToDay(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// offerInactivityBusinessDays is the number of business days (Mon–Fri,
// weekends skipped) of inactivity after which an open OTC offer is
// auto-expired (todoSpec "Automatska promena stanja pregovora": "Nakon
// 3 radna dana").
const offerInactivityBusinessDays = 3

// offerExpiryWarnBusinessDays is the inactivity threshold (in business
// days) at which the pre-expiry warning fires — one business day before
// auto-expiry, so the holder can extend/renew/change the offer.
const offerExpiryWarnBusinessDays = offerInactivityBusinessDays - 1

// subtractBusinessDays returns the wall-clock instant `n` business days
// (Mon–Fri; Sat/Sun skipped) before `from`, preserving the time-of-day.
// n must be >= 0; n == 0 returns `from` unchanged. The walk happens in
// `loc` so the weekday boundaries match the business calendar.
//
// Example: from a Monday, subtracting 3 business days lands on the prior
// Wednesday (Mon→Fri→Thu→Wed, skipping the weekend in between).
func subtractBusinessDays(from time.Time, n int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	t := from.In(loc)
	for remaining := n; remaining > 0; {
		t = t.AddDate(0, 0, -1)
		if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday {
			remaining--
		}
	}
	return t
}

// SweepExpiredOTCContracts walks active contracts whose settlement_date
// is on or before `today` and marks each `expired`, releasing the
// seller's reservation. Also fires a one-shot pre-expiry warning email
// to the buyer for contracts that are exactly expiryWarnDays calendar
// days away (scenario S63). Best-effort per-contract — a failure on one
// row doesn't block the sweep from finishing the rest.
//
// Idempotent: a re-run on the same day is a no-op because the rows
// flipped on the first pass no longer match status='active'.
// The pre-expiry warning naturally fires exactly once: the day-granularity
// check (daysUntil == expiryWarnDays) is true only on one calendar day
// per contract, so repeated cron ticks on the same day hit the same
// contracts but only the 5-minute cadence fires, and the check is
// calendar-day stable within that window (idempotent by design, not by
// a DB flag).
func (s *Service) SweepExpiredOTCContracts(ctx context.Context) (*SweepExpiredOTCContractsResult, error) {
	now := s.now()
	loc := s.Cfg.Belgrade
	if loc == nil {
		loc = time.UTC
	}

	// 1. Expire past-due contracts.
	rows, err := s.Store.ListExpiredOTCContracts(ctx, now)
	if err != nil {
		return nil, err
	}
	out := &SweepExpiredOTCContractsResult{}
	for _, c := range rows {
		if err := s.expireOneOTCContract(ctx, c); err != nil {
			s.Log.Warn("otc expiry: contract sweep failed",
				"contract_id", c.ID, "err", err.Error())
			continue
		}
		out.ContractsExpired++
		out.SharesReleased += c.Quantity
		s.log().InfoContext(ctx, "otc contract expired",
			"contract_id", c.ID, "quantity", c.Quantity, "buyer_id", c.BuyerID)
		if s.OTCNotifier != nil {
			s.OTCNotifier.OnOTCContractExpired(ctx, c, c.BuyerID, c.BuyerKind)
		}
	}

	// 2. Pre-expiry warning: contracts exactly expiryWarnDays days away.
	soon, err := s.Store.ListOTCContractsExpiringSoon(ctx, now, loc, expiryWarnDays)
	if err != nil {
		// Best-effort: log and continue; don't fail the whole sweep.
		s.Log.Warn("otc expiry: pre-expiry query failed", "err", err.Error())
	} else {
		for _, c := range soon {
			days := daysUntilExpiry(now, c.SettlementDate, loc)
			if days != expiryWarnDays {
				// Defensive: store query should guarantee this, but validate.
				continue
			}
			if s.OTCNotifier != nil {
				s.OTCNotifier.OnOTCContractExpiringSoon(ctx, c, c.BuyerID, c.BuyerKind, days)
			}
			out.ContractsWarned++
		}
	}

	// 3. Auto-expire open OFFERS inactive for 3 business days, and warn
	// the holder one business day before they age out (todoSpec C4).
	if err := s.sweepExpiredOTCOffers(ctx, now, loc, out); err != nil {
		// Best-effort: log and continue; the contract sweep above already
		// succeeded and shouldn't be lost over an offer-side failure.
		s.Log.Warn("otc expiry: offer sweep failed", "err", err.Error())
	}

	return out, nil
}

// sweepExpiredOTCOffers auto-expires open offers whose last activity
// (updated_at) is older than the 3-business-day inactivity cutoff, and
// fires a one-shot pre-expiry warning for offers that crossed the
// 2-business-day mark (one business day before auto-expiry).
//
// Business days skip weekends: the cutoffs are computed by walking
// backwards from `now`, counting only Mon–Fri. The warning window is
// the half-open band (expireCutoff, warnCutoff] so an offer is warned
// exactly once as it ages from 2 → 3 business days of inactivity, then
// expired on the following sweep once it crosses the 3-business-day line.
func (s *Service) sweepExpiredOTCOffers(ctx context.Context, now time.Time, loc *time.Location, out *SweepExpiredOTCContractsResult) error {
	expireCutoff := subtractBusinessDays(now, offerInactivityBusinessDays, loc)
	warnCutoff := subtractBusinessDays(now, offerExpiryWarnBusinessDays, loc)

	// Expire stale offers first so they don't also match the warn window.
	stale, err := s.Store.ListStaleOpenOTCOffers(ctx, expireCutoff)
	if err != nil {
		return err
	}
	for _, o := range stale {
		if err := s.expireOneOTCOffer(ctx, o); err != nil {
			s.Log.Warn("otc expiry: offer sweep failed",
				"offer_id", o.ID, "thread_id", o.ThreadID, "err", err.Error())
			continue
		}
		out.OffersExpired++
		s.log().InfoContext(ctx, "otc offer auto-expired",
			"offer_id", o.ID, "thread_id", o.ThreadID)
		if s.OTCNotifier != nil {
			// Notify the party whose turn it was (modified_by is the other
			// side); the offer aged out waiting on them.
			recipient, kind := otherParty(o, o.ModifiedBy)
			s.OTCNotifier.OnOTCOfferStateChanged(ctx, o, recipient, kind)
		}
	}

	// Warn offers that have been inactive 2 business days but not yet 3.
	soon, err := s.Store.ListOpenOTCOffersExpiringSoon(ctx, expireCutoff, warnCutoff)
	if err != nil {
		return err
	}
	for _, o := range soon {
		if s.OTCNotifier != nil {
			recipient, kind := otherParty(o, o.ModifiedBy)
			s.OTCNotifier.OnOTCOfferExpiringSoon(ctx, o, recipient, kind)
		}
		out.OffersWarned++
	}
	return nil
}

// expireOneOTCOffer flips a single open offer to `expired` and releases
// the seller's reservation for that iteration's quantity. Plain
// trading-side tx — no cross-service write.
func (s *Service) expireOneOTCOffer(ctx context.Context, o *domain.OTCOffer) error {
	return s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		if o.Quantity > 0 {
			if _, err := s.Store.DecrementReservedHolding(ctx, tx, o.SellerHoldingID, o.Quantity); err != nil {
				return err
			}
		}
		_, err := s.Store.MarkOTCOfferStatus(ctx, tx, o.ID, domain.OTCStatusExpired)
		return err
	})
}

func (s *Service) expireOneOTCContract(ctx context.Context, c *domain.OTCContract) error {
	return s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		// 1. Decrement reservation. Use the contract's recorded
		// holding-id; if the seller has since sold the position the
		// reservation may already be zero — the CHECK protects us.
		if _, err := s.Store.DecrementReservedHolding(ctx, tx, c.SellerHoldingID, c.Quantity); err != nil {
			return err
		}
		// 2. Flip the contract row.
		_, err := s.Store.MarkOTCContractStatus(ctx, tx, c.ID, domain.OTCContractExpired, "", "", nil)
		return err
	})
}
