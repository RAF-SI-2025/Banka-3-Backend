// Package dividend holds the shared dividend math for the quarterly
// dividend payout (C3 individual holders) and its C4 reuse (fund
// holdings) — the spec mandates an identical mechanism, so both call
// this. Pure functions over *big.Rat (pkg/money) and time.Time; no I/O.
//
// It does not move money or pick accounts — that orchestration lives in
// the trading service. This package owns only: the per-holding amount,
// the capital-gains tax on it, and the quarterly schedule (last business
// day of each quarter).
package dividend

import (
	"math/big"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
)

// taxRate is the capital-gains tax applied to a dividend. The spec
// treats dividends as capital gains, taxed at 15% (same rate as
// realized trading gains).
var taxRate = big.NewRat(15, 100)

// TaxRate returns the dividend capital-gains tax rate as a rational.
func TaxRate() *big.Rat { return new(big.Rat).Set(taxRate) }

// Quarterly returns the dividend owed for holding qty shares priced at
// `price` with annual dividend yield `annualYield` (a decimal fraction,
// e.g. 0.005 for 0.5%): qty × price × (annualYield / 4).
//
// Example (spec S54): Quarterly(50, 200, 0.005) = 50 × 200 × 0.00125 =
// 12.50. A non-positive qty/price/yield yields zero.
func Quarterly(qty int64, price, annualYield *big.Rat) *big.Rat {
	if qty <= 0 || price == nil || annualYield == nil ||
		price.Sign() <= 0 || annualYield.Sign() <= 0 {
		return new(big.Rat)
	}
	perShareYear := money.Mul(price, annualYield)
	perShareQuarter := new(big.Rat).Quo(perShareYear, big.NewRat(4, 1))
	return money.Mul(new(big.Rat).SetInt64(qty), perShareQuarter)
}

// Tax returns the 15% capital-gains tax on a dividend amount. A
// non-positive amount yields zero.
func Tax(amount *big.Rat) *big.Rat {
	if amount == nil || amount.Sign() <= 0 {
		return new(big.Rat)
	}
	return money.Mul(amount, taxRate)
}

// QuarterOf returns the quarter (1–4) containing t's month.
func QuarterOf(t time.Time) int {
	return (int(t.Month())-1)/3 + 1
}

// LastBusinessDayOfQuarter returns midnight on the last business day
// (Mon–Fri) of the given quarter (1–4) in loc. Quarters end Mar/Jun/
// Sep/Dec; a quarter-end falling on a weekend rolls back to the prior
// Friday. (Bank holidays are out of scope — the spec only says
// "poslednji radni dan".)
func LastBusinessDayOfQuarter(year, quarter int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	endMonth := time.Month(quarter * 3) // 3, 6, 9, 12
	// Day 0 of the following month == last calendar day of endMonth.
	last := time.Date(year, endMonth+1, 0, 0, 0, 0, 0, loc)
	for last.Weekday() == time.Saturday || last.Weekday() == time.Sunday {
		last = last.AddDate(0, 0, -1)
	}
	return last
}

// IsLastBusinessDayOfQuarter reports whether t falls on the last
// business day of its quarter (date comparison in t's location), i.e.
// the day the quarterly payout cron should fire.
func IsLastBusinessDayOfQuarter(t time.Time) bool {
	lb := LastBusinessDayOfQuarter(t.Year(), QuarterOf(t), t.Location())
	ty, tm, td := t.Date()
	ly, lm, ld := lb.Date()
	return ty == ly && tm == lm && td == ld
}
