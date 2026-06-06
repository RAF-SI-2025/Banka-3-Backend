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

	return out, nil
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
