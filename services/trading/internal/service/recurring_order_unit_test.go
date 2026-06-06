package service

import (
	"errors"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// TestRecurringModeValid pins the BYAMOUNT/BYQUANTITY mode validation
// behind S47/S48.
func TestRecurringModeValid(t *testing.T) {
	if !domain.RecurringByAmount.Valid() || !domain.RecurringByQuantity.Valid() {
		t.Fatal("BYAMOUNT/BYQUANTITY must be valid")
	}
	if domain.RecurringMode("").Valid() || domain.RecurringMode("WEEKLY").Valid() {
		t.Fatal("empty/unknown mode must be invalid")
	}
}

// TestIsInsufficientFunds pins the S50 skip-detection: only the order
// path's FailedPrecondition "nedovoljn* sredst*" message is treated as a
// skip; other errors (validation, unconfigured actuary, plain strings)
// are not, so genuine problems aren't silently swallowed.
func TestIsInsufficientFunds(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"funds-available message", apperr.FailedPrecondition("nedovoljna sredstva na računu za ovaj nalog"), true},
		{"fund-actor message", apperr.FailedPrecondition("fond nema dovoljno hartija za prodaju"), false},
		{"holding alt message", apperr.FailedPrecondition("nedovoljno sredstava na računu — kupovina je preskočena"), true},
		{"unconfigured actuary", apperr.FailedPrecondition("aktuar nije konfigurisan — kontaktirajte supervizora"), false},
		{"validation error", apperr.Validation("quantity must be positive"), false},
		{"nil error", nil, false},
		{"plain error", errors.New("nedovoljna sredstva"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInsufficientFunds(tc.err); got != tc.want {
				t.Fatalf("isInsufficientFunds(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
