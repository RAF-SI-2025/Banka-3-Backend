package bank

import (
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CapitalGainsCollectionResult summarizes one tax-collection run. AccountsPaid
// is the number of (account_id) buckets fully debited; RowsPaid is the count
// of capital_gains rows marked paid; Insufficient counts buckets we couldn't
// debit because the account didn't have the funds — those rows stay unpaid
// for the next collection (spec p.63 doesn't mandate a forced overdraft, and
// the existing loan-collection cron uses the same "skip and leave unpaid"
// shape).
type CapitalGainsCollectionResult struct {
	Period       string
	AccountsPaid int
	RowsPaid     int
	Insufficient int
	TotalDebtRSD int64
	CollectedRSD int64
}

// CollectCapitalGains debits each placer's account for their unpaid
// capital_gains tax and credits the state's RSD account. Used by the monthly
// cron and by the supervisor "Pokreni obračun" button (spec p.63: "Supervizori
// mogu ručno pokrenuti ovu operaciju putem portala").
//
// Pass period="" to sweep every still-unpaid row regardless of period — useful
// for back-fills where the cron missed a month. A specific YYYY-MM filters to
// that calendar month only.
//
// Each account's debit + credit + paid-at update runs in its own transaction
// so an underfunded placer doesn't block collection from everyone else.
func (s *Server) CollectCapitalGains(period string) (CapitalGainsCollectionResult, error) {
	res := CapitalGainsCollectionResult{Period: period}
	if s.db_gorm == nil {
		return res, status.Error(codes.Internal, "gorm db not initialized")
	}

	stateAccount, err := lookupStateRSDAccount(s.db_gorm)
	if err != nil {
		return res, err
	}

	type bucket struct {
		AccountID int64
		Total     int64
	}
	var buckets []bucket
	q := s.db_gorm.Table("capital_gains").
		Select("account_id, SUM(tax_due) AS total").
		Where("paid_at IS NULL")
	if period != "" {
		q = q.Where("period = ?", period)
	}
	if err := q.Group("account_id").Having("SUM(tax_due) > 0").Scan(&buckets).Error; err != nil {
		return res, status.Errorf(codes.Internal, "%v", err)
	}

	for _, b := range buckets {
		res.TotalDebtRSD += b.Total
		paid, rows, err := s.collectOneAccount(stateAccount, b.AccountID, b.Total, period)
		if err != nil {
			log.Printf("[Cron] capital-gains collect account=%d: %v", b.AccountID, err)
			continue
		}
		if !paid {
			res.Insufficient++
			continue
		}
		res.AccountsPaid++
		res.RowsPaid += rows
		res.CollectedRSD += b.Total
	}
	return res, nil
}

// stateRSDAccount is the destination for tax transfers. Identified by
// companies.tax_code=1 (the state company seeded in seed.sql), with the
// currency pinned to RSD per Napomena 2 on spec p.63.
type stateRSDAccount struct {
	ID     int64
	Number string
}

func lookupStateRSDAccount(db *gorm.DB) (stateRSDAccount, error) {
	var out stateRSDAccount
	err := db.Raw(`
		SELECT a.id, a.number FROM accounts a
		JOIN companies co ON co.id = a.company_id
		WHERE co.tax_code = 1 AND a.currency = 'RSD' AND a.active
		ORDER BY a.id ASC
		LIMIT 1
	`).Scan(&out).Error
	if err != nil {
		return out, status.Errorf(codes.Internal, "%v", err)
	}
	if out.ID == 0 {
		return out, status.Error(codes.FailedPrecondition, "state RSD account missing (tax_code=1)")
	}
	return out, nil
}

// collectOneAccount debits a single placer account for its unpaid tax total
// and credits the state account. Returns (paid, rowsPaid). paid=false signals
// insufficient funds — the rows stay unpaid for the next run.
func (s *Server) collectOneAccount(state stateRSDAccount, accountID, totalRSD int64, period string) (bool, int, error) {
	if totalRSD <= 0 {
		return true, 0, nil
	}

	var paid bool
	var rowsPaid int
	err := s.db_gorm.Transaction(func(tx *gorm.DB) error {
		// Lock the source account to serialize the debit against any concurrent
		// trading/loan flow. Same FOR UPDATE pattern as elsewhere in the bank
		// package — without it a parallel transaction could read a stale
		// balance and over-/under-debit.
		var acc Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&acc, accountID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.NotFound, "source account missing")
			}
			return status.Errorf(codes.Internal, "%v", err)
		}

		debitAmount, err := convertRSDToAccountCcy(tx, acc.Currency, totalRSD)
		if err != nil {
			return err
		}

		// Atomic debit gated on balance: a 0-row update means the placer
		// can't cover the tax this run. We let the cron skip them rather
		// than overdrafting (spec p.63 is silent on overdrafts and our
		// loan-collection cron makes the same call).
		debitRes := tx.Exec(
			`UPDATE accounts SET balance = balance - ? WHERE id = ? AND balance >= ?`,
			debitAmount, accountID, debitAmount,
		)
		if debitRes.Error != nil {
			return status.Errorf(codes.Internal, "%v", debitRes.Error)
		}
		if debitRes.RowsAffected == 0 {
			// Insufficient funds — bail without erroring; outer layer counts
			// these for the supervisor portal summary.
			return errInsufficientFunds
		}

		// Credit the state's RSD account. Spec p.63 Napomena 2: država ima
		// samo RSD račun, so credit is always in RSD regardless of source.
		credRes := tx.Exec(
			`UPDATE accounts SET balance = balance + ? WHERE id = ?`,
			totalRSD, state.ID,
		)
		if credRes.Error != nil {
			return status.Errorf(codes.Internal, "%v", credRes.Error)
		}
		if credRes.RowsAffected == 0 {
			return status.Error(codes.Internal, "state account update affected no rows")
		}

		now := time.Now()
		updateQ := tx.Table("capital_gains").
			Where("account_id = ? AND paid_at IS NULL", accountID)
		if period != "" {
			updateQ = updateQ.Where("period = ?", period)
		}
		mark := updateQ.Update("paid_at", now)
		if mark.Error != nil {
			return status.Errorf(codes.Internal, "%v", mark.Error)
		}
		rowsPaid = int(mark.RowsAffected)
		paid = true
		return nil
	})
	if err != nil {
		if errors.Is(err, errInsufficientFunds) {
			return false, 0, nil
		}
		return false, 0, err
	}
	return paid, rowsPaid, nil
}

// errInsufficientFunds is the sentinel the per-account transaction returns
// when the balance check fails, so the outer collector can roll the tx back
// and continue with the remaining accounts.
var errInsufficientFunds = fmt.Errorf("insufficient funds")

// convertRSDToAccountCcy turns the recorded RSD tax debt back into the source
// account's currency for the debit. RSD accounts skip the rate lookup.
// Rounding is up so the placer never under-pays the recorded RSD amount when
// rates create a fractional result.
func convertRSDToAccountCcy(tx *gorm.DB, accCurrency string, totalRSD int64) (int64, error) {
	if accCurrency == "RSD" {
		return totalRSD, nil
	}
	var rate ExchangeRate
	if err := tx.Where("currency_code = ?", accCurrency).First(&rate).Error; err != nil {
		return 0, status.Errorf(codes.Internal, "exchange rate for %s: %v", accCurrency, err)
	}
	if rate.Rate_to_rsd <= 0 {
		return 0, status.Errorf(codes.Internal, "non-positive exchange rate for %s", accCurrency)
	}
	return int64(math.Ceil(float64(totalRSD) / rate.Rate_to_rsd)), nil
}

// RunMonthlyCapitalGainsCollection is the cron entrypoint — kicks off the
// collection for the current calendar month. Logged the same way as the
// loan-collection cron so operators can scrape the same prefix.
func (s *Server) RunMonthlyCapitalGainsCollection() {
	period := time.Now().Format("2006-01")
	log.Printf("[Cron] Running capital-gains collection for %s", period)
	res, err := s.CollectCapitalGains(period)
	if err != nil {
		log.Printf("[Cron] ERROR collecting capital gains: %v", err)
		return
	}
	log.Printf("[Cron] Capital-gains %s: paid=%d accounts (%d rows, %d RSD), insufficient=%d (%d RSD outstanding)",
		res.Period, res.AccountsPaid, res.RowsPaid, res.CollectedRSD, res.Insufficient, res.TotalDebtRSD-res.CollectedRSD)
}
