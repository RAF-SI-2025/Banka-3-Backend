package trading

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// Commission capAmounts in native-currency minor units (spec pp. 51–52). We treat
// "$7" / "$12" as 7.00 / 12.00 in the instrument's currency — matching how
// price_per_unit is stored (e.g. forex `int64(ExchangeRate * 100)`).
const (
	commissionCapMarket int64 = 700
	commissionCapLimit  int64 = 1200
	// Percentages are taken as integer permille to avoid float rounding:
	// 14% = 140‰, 24% = 240‰.
	commissionPermilleMarket int64 = 140
	commissionPermilleLimit  int64 = 240
)

// bankSystemOwnerEmail is the system client whose per-currency internal
// accounts act as the bank's fee-collection accounts (seeded in seed.sql).
const bankSystemOwnerEmail = "system@banka3.rs"

// computeCommission returns the commission in the instrument's currency minor
// units. Stop orders bill like market (they become market at trigger),
// stop_limit bills like limit (spec p. 51).
func computeCommission(ot OrderType, approxNative int64) int64 {
	var permille, capAmount int64
	switch ot {
	case OrderMarket, OrderStop:
		permille, capAmount = commissionPermilleMarket, commissionCapMarket
	case OrderLimit, OrderStopLimit:
		permille, capAmount = commissionPermilleLimit, commissionCapLimit
	default:
		return 0
	}
	pct := (approxNative * permille) / 1000
	if pct < capAmount {
		return pct
	}
	return capAmount
}

// chargeCommission debits the placer's account and credits the bank's
// fee-collection account in the same currency, atomically inside the caller's
// transaction. Returns FailedPrecondition if the placer cannot cover the fee,
// FailedPrecondition if no bank fee account exists for the currency.
func chargeCommission(tx *gorm.DB, debitAccount, currency string, amount int64) error {
	if amount <= 0 {
		return nil
	}

	// Debit placer with a balance guard so overdrafts fail loudly rather than
	// silently driving the account negative.
	res := tx.Exec(
		`UPDATE accounts SET balance = balance - ? WHERE number = ? AND balance >= ?`,
		amount, debitAccount, amount,
	)
	if res.Error != nil {
		return status.Errorf(codes.Internal, "%v", res.Error)
	}
	if res.RowsAffected == 0 {
		return status.Error(codes.FailedPrecondition, "insufficient funds for commission")
	}

	// Credit the bank's fee-collection account for this currency. Seeded per
	// currency under the system client; see seed.sql.
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
			return status.Errorf(codes.FailedPrecondition, "no fee-collection account for %s", currency)
		}
		return status.Errorf(codes.Internal, "%v", err)
	}
	if feeAccount == "" {
		return status.Errorf(codes.FailedPrecondition, "no fee-collection account for %s", currency)
	}

	res = tx.Exec(
		`UPDATE accounts SET balance = balance + ? WHERE number = ?`,
		amount, feeAccount,
	)
	if res.Error != nil {
		return status.Errorf(codes.Internal, "%v", res.Error)
	}
	if res.RowsAffected == 0 {
		return status.Errorf(codes.FailedPrecondition, "no fee-collection account for %s", currency)
	}
	return nil
}
