// Package account holds the 18-digit account-number format used by
// every bank in the system. The format is BBB FFFF NNNNNNNNN TT:
// bank code (3) + branch (4) + random body (9) + type (2). The number
// is valid iff the sum of all 18 digits is divisible by 11. C5
// inter-bank lookup leans on this — every bank's generator and
// validator must agree.
package account

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
)

const (
	// Number is always 18 digits.
	Length = 18

	bankCodeLen = 3
	branchLen   = 4
	bodyLen     = 9
	typeLen     = 2
)

// Type encodes the trailing two-digit account-type code. Values are
// frozen by the spec (Banka2025.pdf p.16) so every bank in the system
// shares the same suffix-to-meaning mapping. New account types must
// pick an unused two-digit value; never repurpose.
//
// Spec p.16:
//
//	Tekući račun bucket (1*):
//	  11  lični standardni
//	  12  poslovni (DOO/AD/Fondacija collapse here)
//	  13  štedni
//	  14  penzionerski
//	  15  za mlade
//	  16  za studente
//	  17  za nezaposlene
//	Devizni račun bucket (2*):
//	  21  lični
//	  22  poslovni
//	System bucket (9*):
//	  99  bank-owned house account (not in spec — internal sentinel)
type Type int

const (
	// Tekući bucket — RSD only.
	TypePersonalChecking Type = 11
	TypeBusinessChecking Type = 12
	TypeSavings          Type = 13
	TypePensioner        Type = 14
	TypeYouth            Type = 15
	TypeStudent          Type = 16
	TypeUnemployed       Type = 17

	// Devizni bucket — one of the seven supported FX currencies.
	TypePersonalFX Type = 21
	TypeBusinessFX Type = 22

	// System bucket — bank's own house accounts (not in spec).
	TypeSystem Type = 99
)

// Validate reports whether s is a syntactically valid account number:
// 18 digits, mod-11 checksum holds. Returns parts on success.
func Validate(s string) (Parts, error) {
	if len(s) != Length {
		return Parts{}, fmt.Errorf("account number: want %d digits, got %d", Length, len(s))
	}
	var sum int
	for i, r := range s {
		if r < '0' || r > '9' {
			return Parts{}, fmt.Errorf("account number: non-digit at position %d", i)
		}
		sum += int(r - '0')
	}
	if sum%11 != 0 {
		return Parts{}, errors.New("account number: checksum mismatch (sum mod 11 ≠ 0)")
	}
	return Parts{
		BankCode: s[:bankCodeLen],
		Branch:   s[bankCodeLen : bankCodeLen+branchLen],
		Body:     s[bankCodeLen+branchLen : bankCodeLen+branchLen+bodyLen],
		Type:     mustType(s[Length-typeLen:]),
		Raw:      s,
	}, nil
}

// Parts is the structured view of a parsed account number.
type Parts struct {
	BankCode string
	Branch   string
	Body     string
	Type     Type
	Raw      string
}

// Generate returns a fresh 18-digit number for (bankCode, branch, t).
// bankCode must be 3 digits, branch must be 4 digits. The body is
// chosen with cryptographic randomness so two callers don't collide
// and so the body itself reveals no allocation order; the last body
// digit is fixed to satisfy the mod-11 checksum.
//
// The mod-11 trick: pick 8 random body digits, then the 9th must equal
// (11 - sumSoFar mod 11) mod 11. If that resolves to 10 (impossible as
// a single digit) we re-roll — happens ~1/11 of the time.
func Generate(bankCode, branch string, t Type) (string, error) {
	if len(bankCode) != bankCodeLen || !allDigits(bankCode) {
		return "", fmt.Errorf("bank code must be %d digits", bankCodeLen)
	}
	if len(branch) != branchLen || !allDigits(branch) {
		return "", fmt.Errorf("branch must be %d digits", branchLen)
	}
	if t < 0 || t > 99 {
		return "", fmt.Errorf("type %d out of range", t)
	}
	typeStr := fmt.Sprintf("%02d", t)

	// We loop because the mod-11 fixup occasionally needs a digit ≥ 10.
	// In practice this terminates within a handful of attempts.
	for attempt := 0; attempt < 32; attempt++ {
		body, err := randomDigits(bodyLen - 1)
		if err != nil {
			return "", err
		}
		head := bankCode + branch + body
		sumHead := digitSum(head + typeStr)
		need := (11 - (sumHead % 11)) % 11
		if need == 10 {
			continue // try again with a different body
		}
		final := head + fmt.Sprintf("%d", need) + typeStr
		// Sanity-check our own work — cheap, catches a future bug fast.
		if _, err := Validate(final); err != nil {
			return "", fmt.Errorf("internal: generated invalid number: %w", err)
		}
		return final, nil
	}
	return "", errors.New("account number: gave up generating a checksum-clean number")
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func digitSum(s string) int {
	var sum int
	for _, r := range s {
		sum += int(r - '0')
	}
	return sum
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

func mustType(s string) Type {
	var v int
	for _, r := range s {
		v = v*10 + int(r-'0')
	}
	return Type(v)
}
