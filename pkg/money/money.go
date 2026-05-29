// Package money holds decimal arithmetic helpers used by the bank
// service. Money is parsed from strings, computed with *big.Rat for
// exactness, and serialized back to a fixed-precision string so JSON
// callers see e.g. "117.50" rather than "117.5".
//
// We deliberately avoid float64 for currency math.
package money

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// AmountScale is the number of fractional digits we render for amounts
// (RSD/USD/etc are 2; we keep 4 internally to absorb intermediate FX
// products without rounding loss). Rates are 8.
const (
	AmountScale = 4
	RateScale   = 8
)

// Parse reads a decimal string into a *big.Rat. Empty / "0" both
// return zero. Surfaces a clear error on garbage input.
func Parse(s string) (*big.Rat, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "0"
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("money: %q is not a decimal", s)
	}
	return r, nil
}

// MustParse panics on error — for compile-time-known constants only.
func MustParse(s string) *big.Rat {
	r, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return r
}

// Format renders r with `scale` fractional digits, rounded to nearest
// (banker's rounding via big.Rat.FloatString).
func Format(r *big.Rat, scale int) string {
	return r.FloatString(scale)
}

// FormatAmount is shorthand for the canonical amount scale.
func FormatAmount(r *big.Rat) string { return Format(r, AmountScale) }

// FormatRate is shorthand for the canonical rate scale.
func FormatRate(r *big.Rat) string { return Format(r, RateScale) }

// Mul returns a*b without mutating its inputs.
func Mul(a, b *big.Rat) *big.Rat { return new(big.Rat).Mul(a, b) }

// Div returns a/b. Returns an error if b is zero.
func Div(a, b *big.Rat) (*big.Rat, error) {
	if b.Sign() == 0 {
		return nil, errors.New("money: divide by zero")
	}
	bi := new(big.Rat).Inv(b)
	return new(big.Rat).Mul(a, bi), nil
}

// Add returns a+b.
func Add(a, b *big.Rat) *big.Rat { return new(big.Rat).Add(a, b) }

// Sub returns a-b.
func Sub(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }

// Cmp returns sign(a - b): -1, 0, +1.
func Cmp(a, b *big.Rat) int { return a.Cmp(b) }

// IsPositive reports whether r > 0.
func IsPositive(r *big.Rat) bool { return r.Sign() > 0 }

// IsNonNegative reports whether r >= 0.
func IsNonNegative(r *big.Rat) bool { return r.Sign() >= 0 }
