// Package loans holds the loan-rate lookup tables and the annuity
// formula. Pure logic; no I/O.
//
// Spec references:
//   - p.33 base rates by loan amount (RSD-equivalent)
//   - p.33 margin by loan type
//   - p.34 annuity formula and variable-rate offset (±1.50%)
package loans

import (
	"math/big"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
)

// Type is the loan kind. Values match the bank-service domain.
type Type string

const (
	TypeCash      Type = "cash"
	TypeHousing   Type = "housing"
	TypeAuto      Type = "auto"
	TypeRefinance Type = "refinance"
	TypeStudent   Type = "student"
)

// InterestType discriminates between fixed and variable.
type InterestType string

const (
	Fixed    InterestType = "fixed"
	Variable InterestType = "variable"
)

// PomerajRange is the absolute bound on the variable-rate offset.
// Spec p.33: pomeraj is in [-1.50%, +1.50%] generated monthly.
const PomerajRange = "1.50"

// AllowedInstallments returns the installment counts the FE form
// must offer for this loan type, per spec p.31. Empty slice means
// any positive integer is accepted; we never return that today.
func AllowedInstallments(t Type) []int {
	switch t {
	case TypeHousing:
		return []int{60, 120, 180, 240, 300, 360}
	case TypeCash, TypeAuto, TypeRefinance, TypeStudent:
		return []int{12, 24, 36, 48, 60, 72, 84}
	}
	return nil
}

// IsAllowedInstallments reports whether n is in AllowedInstallments(t).
func IsAllowedInstallments(t Type, n int) bool {
	for _, v := range AllowedInstallments(t) {
		if v == n {
			return true
		}
	}
	return false
}

// BaseRate returns the annual base rate (osnovica) in percent for a
// loan of `amountRSD` (already converted to RSD-equivalent).
//
// Brackets are inclusive on the upper bound to make the boundary
// explicit; spec p.33 uses "0–500.000", "500.001–1.000.000", etc., so
// 500.000 belongs to the 6.25% row, 500.001 to 6.00%.
func BaseRate(amountRSD *big.Rat) *big.Rat {
	r := func(s string) *big.Rat { return money.MustParse(s) }
	switch {
	case amountRSD.Cmp(r("500000")) <= 0:
		return r("6.25")
	case amountRSD.Cmp(r("1000000")) <= 0:
		return r("6.00")
	case amountRSD.Cmp(r("2000000")) <= 0:
		return r("5.75")
	case amountRSD.Cmp(r("5000000")) <= 0:
		return r("5.50")
	case amountRSD.Cmp(r("10000000")) <= 0:
		return r("5.25")
	case amountRSD.Cmp(r("20000000")) <= 0:
		return r("5.00")
	}
	return r("4.75")
}

// Margin returns the bank's margin (M) for a given loan type, in
// annual percent.
func Margin(t Type) *big.Rat {
	switch t {
	case TypeCash:
		return money.MustParse("1.75")
	case TypeHousing:
		return money.MustParse("1.50")
	case TypeAuto:
		return money.MustParse("1.25")
	case TypeRefinance:
		return money.MustParse("1.00")
	case TypeStudent:
		return money.MustParse("0.75")
	}
	return money.MustParse("1.75") // sane default
}

// MonthlyRate composes the per-month rate from the annual components.
// The result is unit-less (i.e. 0.005208… for 6.25% annual / 12).
func MonthlyRate(baseAnnualPct, offsetAnnualPct, marginAnnualPct *big.Rat) *big.Rat {
	annualPct := money.Add(money.Add(baseAnnualPct, offsetAnnualPct), marginAnnualPct)
	monthly, _ := money.Div(annualPct, money.MustParse("1200")) // /100 then /12
	return monthly
}

// Annuity computes the per-installment amount A using the standard
// fully-amortising formula:
//
//	A = P × r(1+r)^n / ((1+r)^n − 1)
//
// monthlyRate is unit-less (e.g. 0.00521); n is the total number of
// installments. Returns the annuity in the same currency as principal.
//
// Edge case: r == 0 → A = P / n (interest-free loan, even payment).
func Annuity(principal, monthlyRate *big.Rat, n int) *big.Rat {
	if monthlyRate.Sign() == 0 || n <= 0 {
		if n <= 0 {
			n = 1
		}
		out, _ := money.Div(principal, big.NewRat(int64(n), 1))
		return out
	}
	one := money.MustParse("1")
	pow := powInt(money.Add(one, monthlyRate), n)
	num := money.Mul(monthlyRate, pow)
	den := money.Sub(pow, one)
	frac, _ := money.Div(num, den)
	return money.Mul(principal, frac)
}

// powInt is exact-arithmetic exponentiation (we stay in *big.Rat
// throughout; for n up to 360 it's cheap).
func powInt(base *big.Rat, n int) *big.Rat {
	out := money.MustParse("1")
	for i := 0; i < n; i++ {
		out = money.Mul(out, base)
	}
	return out
}
