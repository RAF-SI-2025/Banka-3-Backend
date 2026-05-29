// Package card holds card-number formatting helpers: brand detection
// from the leading digits + Luhn checksum + a generator that yields a
// brand-correct, Luhn-clean number.
//
// Spec p.27: cards are 16 digits (or 15 for Amex). The MII (digit 1)
// names the issuer sector; digits 2-6 are the IIN (this bank's ID,
// 5 digits); digits 7-15 are the unique account ID; digit 16 is the
// Luhn check digit. We pick a brand at creation time and fix the
// matching prefix.
package card

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Brand is the card scheme. Each brand fixes the leading digits and,
// for Amex, the total length.
type Brand string

const (
	BrandVisa       Brand = "visa"
	BrandMastercard Brand = "mastercard"
	BrandDinacard   Brand = "dinacard"
	BrandAmex       Brand = "amex"
)

// Length16 is the standard length; Amex is 15.
const (
	Length16 = 16
	Length15 = 15
)

// DetectBrand returns the brand of n based on its leading digits.
// Returns "" if no brand matches. Useful when validating card numbers
// the user typed in by hand.
func DetectBrand(n string) Brand {
	if len(n) == 0 {
		return ""
	}
	switch {
	case n[0] == '4':
		return BrandVisa
	case strings.HasPrefix(n, "9891"):
		return BrandDinacard
	case strings.HasPrefix(n, "34"), strings.HasPrefix(n, "37"):
		return BrandAmex
	case len(n) >= 2:
		// Mastercard: 51..55 (legacy) or 2221..2720.
		if v2, err := strconv.Atoi(n[:2]); err == nil && v2 >= 51 && v2 <= 55 {
			return BrandMastercard
		}
		if len(n) >= 4 {
			if v4, err := strconv.Atoi(n[:4]); err == nil && v4 >= 2221 && v4 <= 2720 {
				return BrandMastercard
			}
		}
	}
	return ""
}

// LengthOf returns the canonical card length for a brand.
func LengthOf(b Brand) int {
	if b == BrandAmex {
		return Length15
	}
	return Length16
}

// Validate reports whether n is a syntactically valid card number for
// some known brand: length matches the brand, all digits, and Luhn
// checksum holds.
func Validate(n string) (Brand, error) {
	if !allDigits(n) {
		return "", errors.New("card number: non-digit character")
	}
	b := DetectBrand(n)
	if b == "" {
		return "", errors.New("card number: unknown brand prefix")
	}
	if want := LengthOf(b); len(n) != want {
		return "", fmt.Errorf("card number: %s expects %d digits, got %d", b, want, len(n))
	}
	if !luhnOK(n) {
		return "", errors.New("card number: Luhn checksum failed")
	}
	return b, nil
}

// Generate returns a fresh card number for brand b. The body digits
// (everything between the fixed prefix and the Luhn check digit) are
// chosen with crypto/rand; the last digit is computed to satisfy
// Luhn. Two calls in a row will not collide except by birthday-paradox
// odds against random 10^9-ish space — the caller is expected to
// retry on a unique-violation insert.
func Generate(b Brand) (string, error) {
	prefix, err := pickPrefix(b)
	if err != nil {
		return "", err
	}
	totalLen := LengthOf(b)
	bodyLen := totalLen - len(prefix) - 1 // -1 for the trailing check digit
	body, err := randomDigits(bodyLen)
	if err != nil {
		return "", err
	}
	head := prefix + body
	check := luhnCheckDigit(head)
	out := head + fmt.Sprintf("%d", check)
	if _, err := Validate(out); err != nil {
		return "", fmt.Errorf("internal: generated invalid card number: %w", err)
	}
	return out, nil
}

// Mask returns "5798********5571" — the public-display form per spec
// p.29 ("prve 4 cifre, potom 8 zvezdica i zadnje 4 cifre").
func Mask(n string) string {
	if len(n) < 8 {
		return strings.Repeat("*", len(n))
	}
	stars := len(n) - 8
	return n[:4] + strings.Repeat("*", stars) + n[len(n)-4:]
}

// =====================================================================
// internal
// =====================================================================

func pickPrefix(b Brand) (string, error) {
	switch b {
	case BrandVisa:
		return "4", nil
	case BrandMastercard:
		// Pick one of 51..55. We could also use the 2221-2720 range,
		// but a 2-digit prefix gives more body entropy, so prefer it.
		v, err := rand.Int(rand.Reader, big.NewInt(5))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("5%d", 1+v.Int64()), nil
	case BrandDinacard:
		return "9891", nil
	case BrandAmex:
		// 34 or 37.
		v, err := rand.Int(rand.Reader, big.NewInt(2))
		if err != nil {
			return "", err
		}
		if v.Int64() == 0 {
			return "34", nil
		}
		return "37", nil
	}
	return "", fmt.Errorf("card: unknown brand %q", b)
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func randomDigits(n int) (string, error) {
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		out[i] = byte('0' + v.Int64())
	}
	return string(out), nil
}

// luhnOK reports whether the full string (including check digit)
// passes the Luhn checksum.
func luhnOK(s string) bool {
	sum := 0
	parity := len(s) % 2
	for i, r := range s {
		d := int(r - '0')
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// luhnCheckDigit returns the digit that, appended to head, makes the
// full string Luhn-clean.
func luhnCheckDigit(head string) int {
	sum := 0
	parity := (len(head) + 1) % 2 // parity for the *full* string
	for i, r := range head {
		d := int(r - '0')
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return (10 - (sum % 10)) % 10
}
