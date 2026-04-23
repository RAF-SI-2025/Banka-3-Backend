package trading

import (
	"errors"
	"math"

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
	// menjacnicaCommissionPermille mirrors bank.commission_rate (1%). Applied
	// to the account-currency equivalent of a cross-currency debit when the
	// placer is a client (spec pp.27, 57).
	menjacnicaCommissionPermille int64 = 10
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

// commissionPlan captures the bookkeeping for charging a placement commission
// across potentially different account and instrument currencies. Same-currency
// orders leave InstrumentCurrency == DebitCurrency, FeeInstrument == DebitAmount,
// and MenjacnicaFee == 0 so chargeCommission collapses to a single debit/credit
// pair. Cross-currency orders additionally pocket a menjacnica commission in
// the account's currency (clients only — spec pp.27, 57).
type commissionPlan struct {
	DebitAccount       string
	DebitCurrency      string
	DebitAmount        int64
	InstrumentCurrency string
	FeeInstrument      int64
	MenjacnicaFee      int64
}

// planCommissionCharge derives how much to pull from the placer's account and
// how much to credit each fee pool, given the instrument-currency commission
// and the account/instrument exchange rates. Rates are RSD-per-unit so a
// conversion from instrument to account currency is `x * rateInstrRSD /
// rateAccRSD`. Clients eat a 1% menjacnica fee on the converted amount
// (isClient=true); employee placers pay no conversion fee — bank-owned
// accounts are debited in the employee case too (spec p.57).
func planCommissionCharge(
	debitAccount, debitCurrency, instrumentCurrency string,
	feeInstrument int64,
	rateAccRSD, rateInstrRSD float64,
	isClient bool,
) commissionPlan {
	plan := commissionPlan{
		DebitAccount:       debitAccount,
		DebitCurrency:      debitCurrency,
		InstrumentCurrency: instrumentCurrency,
		FeeInstrument:      feeInstrument,
	}
	if feeInstrument <= 0 {
		return plan
	}
	if debitCurrency == instrumentCurrency {
		plan.DebitAmount = feeInstrument
		return plan
	}
	// Round up so rounding never under-charges the placer.
	feeInAccount := int64(math.Ceil(float64(feeInstrument) * rateInstrRSD / rateAccRSD))
	var menjacnica int64
	if isClient {
		menjacnica = (feeInAccount*menjacnicaCommissionPermille + 999) / 1000
	}
	plan.DebitAmount = feeInAccount + menjacnica
	plan.MenjacnicaFee = menjacnica
	return plan
}

// chargeCommission debits the placer's account in its own currency and credits
// the bank's fee-collection accounts: the instrument-currency pool for the
// order commission and, for cross-currency client orders, the account-currency
// pool for the menjacnica commission. Runs inside the caller's transaction.
// Returns FailedPrecondition if the placer cannot cover the debit or a fee
// pool for the needed currency does not exist.
func chargeCommission(tx *gorm.DB, p commissionPlan) error {
	if p.DebitAmount <= 0 {
		return nil
	}

	// Debit placer with a balance guard so overdrafts fail loudly rather than
	// silently driving the account negative.
	res := tx.Exec(
		`UPDATE accounts SET balance = balance - ? WHERE number = ? AND balance >= ?`,
		p.DebitAmount, p.DebitAccount, p.DebitAmount,
	)
	if res.Error != nil {
		return status.Errorf(codes.Internal, "%v", res.Error)
	}
	if res.RowsAffected == 0 {
		return status.Error(codes.FailedPrecondition, "insufficient funds for commission")
	}

	if p.FeeInstrument > 0 {
		if err := creditFeeAccount(tx, p.InstrumentCurrency, p.FeeInstrument); err != nil {
			return err
		}
	}
	if p.MenjacnicaFee > 0 {
		if err := creditFeeAccount(tx, p.DebitCurrency, p.MenjacnicaFee); err != nil {
			return err
		}
	}
	return nil
}

// creditFeeAccount adds `amount` to the bank's system account for `currency`.
// System accounts are seeded per supported currency (seed.sql) and act as both
// the commission collector and the menjacnica intermediary.
func creditFeeAccount(tx *gorm.DB, currency string, amount int64) error {
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

	res := tx.Exec(
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
