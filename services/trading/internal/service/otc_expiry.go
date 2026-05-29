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

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

// SweepExpiredOTCContractsResult bundles cron telemetry.
type SweepExpiredOTCContractsResult struct {
	ContractsExpired int
	SharesReleased   int32
}

// SweepExpiredOTCContracts walks active contracts whose settlement_date
// is on or before `today` and marks each `expired`, releasing the
// seller's reservation. Best-effort per-contract — a failure on one
// row doesn't block the sweep from finishing the rest.
//
// Idempotent: a re-run on the same day is a no-op because the rows
// flipped on the first pass no longer match status='active'.
func (s *Service) SweepExpiredOTCContracts(ctx context.Context) (*SweepExpiredOTCContractsResult, error) {
	today := s.now()
	rows, err := s.Store.ListExpiredOTCContracts(ctx, today)
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
