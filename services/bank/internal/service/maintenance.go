package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// MaintenanceFeeJobResult summarises one cron run.
type MaintenanceFeeJobResult struct {
	Processed int // accounts evaluated
	Charged   int // accounts where the fee was successfully debited
	Skipped   int // accounts skipped (insufficient funds, no system account, etc.)
}

// RunMaintenanceFeeJob debits the per-spec monthly maintenance fee from
// every active account whose last_maintenance_debit is older than 30
// days. The fee moves into the bank's same-currency house account; we
// don't charge cross-currency since the spec only assigns fees to RSD
// checking accounts (FX accounts come in fee-free).
//
// Idempotent under the same `now`: running the job twice on the same
// day won't double-charge because last_maintenance_debit was already
// stamped on the first run.
//
// Admin-only when invoked manually; the cron path uses RunMaintenanceFeeJobAuto.
func (s *Service) RunMaintenanceFeeJob(ctx context.Context, now time.Time) (*MaintenanceFeeJobResult, error) {
	if err := s.requirePermission(ctx, permissions.Admin); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	return s.runMaintenanceFeeJob(ctx, now)
}

// RunMaintenanceFeeJobAuto is the un-authenticated entry used by the
// in-process cron.
func (s *Service) RunMaintenanceFeeJobAuto(ctx context.Context) error {
	res, err := s.runMaintenanceFeeJob(ctx, time.Now())
	if err != nil {
		return err
	}
	s.Log.Info("maintenance fee job ran",
		"processed", res.Processed, "charged", res.Charged, "skipped", res.Skipped)
	return nil
}

// monthlyDebitCutoff is the threshold for "due now": accounts whose
// last debit is older than this, or that have never been debited, are
// considered due. 28 days is the smallest safe cutoff to ensure every
// month gets exactly one debit no matter when the cron ticks.
const monthlyDebitCutoff = 28 * 24 * time.Hour

func (s *Service) runMaintenanceFeeJob(ctx context.Context, now time.Time) (*MaintenanceFeeJobResult, error) {
	cutoff := now.Add(-monthlyDebitCutoff)
	due, err := s.Store.ListAccountsDueForMaintenance(ctx, cutoff)
	if err != nil {
		return nil, err
	}
	res := &MaintenanceFeeJobResult{Processed: len(due)}
	for _, a := range due {
		if err := s.chargeMaintenance(ctx, a); err != nil {
			res.Skipped++
			s.Log.Warn("maintenance fee skipped",
				"account_id", a.ID, "currency", a.Currency, "fee", a.MaintenanceFee, "error", err)
			continue
		}
		res.Charged++
	}
	return res, nil
}

// chargeMaintenance debits the fee from `a` into the same-currency
// house account, writes a ledger row (op_kind='fee'), and stamps
// last_maintenance_debit. All in one tx so a partial failure doesn't
// leave the books inconsistent.
func (s *Service) chargeMaintenance(ctx context.Context, a *domain.Account) error {
	house, err := s.Store.GetSystemAccount(ctx, a.Currency)
	if err != nil {
		return err
	}
	op := uuid.NewString()
	return s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		// Insufficient funds → leave the row untouched, mark skip.
		if err := s.Store.CheckLimits(ctx, tx, a.ID, a.MaintenanceFee); err != nil {
			return err
		}
		if err := s.Store.AdjustBalance(ctx, tx, a.ID, "-"+a.MaintenanceFee); err != nil {
			return apperr.Internal("debit fee", err)
		}
		if err := s.Store.AdjustBalance(ctx, tx, house.ID, a.MaintenanceFee); err != nil {
			return apperr.Internal("credit house", err)
		}
		_, err := s.Store.InsertTransaction(ctx, tx, &domain.Transaction{
			OpID:          op,
			Kind:          domain.TxKindFee,
			LegIndex:      0,
			FromAccountID: a.ID,
			ToAccountID:   house.ID,
			FromAmount:    a.MaintenanceFee,
			ToAmount:      a.MaintenanceFee,
			Purpose:       "Mesečno održavanje računa",
			Status:        domain.TxStatusRealized,
		})
		if err != nil {
			return err
		}
		return s.Store.MarkMaintenanceDebited(ctx, tx, a.ID)
	})
}
