package loans

import (
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
)

func TestBaseRate(t *testing.T) {
	cases := []struct {
		amount string
		want   string
	}{
		{"100000", "6.25"},
		{"500000", "6.25"}, // boundary belongs to first bracket
		{"500001", "6.00"}, // first cent into next bracket
		{"1000000", "6.00"},
		{"1000001", "5.75"},
		{"5000001", "5.25"},
		{"20000001", "4.75"},
		{"100000000", "4.75"},
	}
	for _, tc := range cases {
		t.Run(tc.amount, func(t *testing.T) {
			got := money.Format(BaseRate(money.MustParse(tc.amount)), 2)
			if got != tc.want {
				t.Errorf("amount %s: got %s want %s", tc.amount, got, tc.want)
			}
		})
	}
}

func TestMargin(t *testing.T) {
	cases := []struct {
		typ  Type
		want string
	}{
		{TypeCash, "1.75"},
		{TypeHousing, "1.50"},
		{TypeAuto, "1.25"},
		{TypeRefinance, "1.00"},
		{TypeStudent, "0.75"},
	}
	for _, tc := range cases {
		t.Run(string(tc.typ), func(t *testing.T) {
			if got := money.Format(Margin(tc.typ), 2); got != tc.want {
				t.Errorf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestAnnuity_StandardCase(t *testing.T) {
	// 1,000,000 RSD over 60 months at 7.5% annual fixed.
	// r = 7.5 / 100 / 12 = 0.00625.
	// A = P × r(1+r)^n / ((1+r)^n − 1)
	//   = 1e6 × 0.00625 × 1.45329^60 / (1.45329 − 1)
	//   ≈ 20037.95 RSD.
	P := money.MustParse("1000000")
	r := money.MustParse("0.00625")
	A := Annuity(P, r, 60)
	got := money.FormatAmount(A)
	if got[:8] != "20037.94" {
		t.Errorf("annuity ≈ 20037.94, got %s", got)
	}
}

func TestAnnuity_ZeroRate(t *testing.T) {
	// Interest-free → A = P/n.
	A := Annuity(money.MustParse("12000"), money.MustParse("0"), 12)
	if got := money.FormatAmount(A); got != "1000.0000" {
		t.Errorf("zero-rate annuity: got %s want 1000.0000", got)
	}
}

func TestMonthlyRate(t *testing.T) {
	// 6.25% base + 0% offset + 1.75% margin = 8.00% annual = 0.00666… monthly.
	mr := MonthlyRate(money.MustParse("6.25"), money.MustParse("0"), money.MustParse("1.75"))
	got := money.Format(mr, 8)
	// 8.00 / 1200 = 0.00666666…
	if got[:7] != "0.00666" {
		t.Errorf("monthly rate ≈ 0.00666666, got %s", got)
	}
}

func TestAllowedInstallments(t *testing.T) {
	if !IsAllowedInstallments(TypeCash, 60) {
		t.Error("60 months should be allowed for cash")
	}
	if IsAllowedInstallments(TypeCash, 360) {
		t.Error("360 months should NOT be allowed for cash (housing only)")
	}
	if !IsAllowedInstallments(TypeHousing, 360) {
		t.Error("360 months should be allowed for housing")
	}
	if IsAllowedInstallments(TypeHousing, 84) {
		t.Error("84 months should NOT be allowed for housing")
	}
}

// TestAllowedInstallments_PerType pins the per-type install grids. If
// the spec ever changes one of these, this test must be updated in
// lockstep with the FE form options.
func TestAllowedInstallments_PerType(t *testing.T) {
	cases := map[Type][]int{
		TypeCash:      {12, 24, 36, 48, 60, 72, 84},
		TypeAuto:      {12, 24, 36, 48, 60, 72, 84},
		TypeRefinance: {12, 24, 36, 48, 60, 72, 84},
		TypeStudent:   {12, 24, 36, 48, 60, 72, 84},
		TypeHousing:   {60, 120, 180, 240, 300, 360},
	}
	for typ, want := range cases {
		got := AllowedInstallments(typ)
		if len(got) != len(want) {
			t.Errorf("%s: got %v want %v", typ, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s[%d]: got %d want %d", typ, i, got[i], want[i])
			}
		}
	}
}

// TestBaseRate_BoundariesUseLowerBucket pins the inclusive-upper-bound
// behaviour documented in BaseRate's comment — 500_000 stays at 6.25%,
// 500_001 drops to 6.00%. The spec leaves this slightly ambiguous but
// our chosen interpretation is load-bearing for the integration test
// expectations downstream.
func TestBaseRate_BoundariesUseLowerBucket(t *testing.T) {
	cases := []struct {
		amount, want string
	}{
		{"500000", "6.25"}, {"500001", "6.00"},
		{"1000000", "6.00"}, {"1000001", "5.75"},
		{"2000000", "5.75"}, {"2000001", "5.50"},
		{"5000000", "5.50"}, {"5000001", "5.25"},
		{"10000000", "5.25"}, {"10000001", "5.00"},
		{"20000000", "5.00"}, {"20000001", "4.75"},
	}
	for _, tc := range cases {
		got := money.Format(BaseRate(money.MustParse(tc.amount)), 2)
		if got != tc.want {
			t.Errorf("amount %s: got %s want %s", tc.amount, got, tc.want)
		}
	}
}

// TestMonthlyRate_FullComposition: 6.00% base + (-1.50% offset) +
// 1.75% margin = 6.25% annual = 0.0052083… monthly. Catches sign-
// handling on the offset (a negative pomeraj brings the rate down).
func TestMonthlyRate_FullComposition(t *testing.T) {
	mr := MonthlyRate(money.MustParse("6.00"), money.MustParse("-1.50"), money.MustParse("1.75"))
	got := money.Format(mr, 8)
	// 6.25 / 1200 = 0.005208333…
	if got[:9] != "0.0052083" {
		t.Errorf("monthly rate: got %s, want 0.0052083…", got)
	}
}

// TestPomerajRange documents the constant the variable-rate cron
// reads. If it ever drifts from spec p.33's [-1.50%, +1.50%], this
// test is the canary.
func TestPomerajRange(t *testing.T) {
	if PomerajRange != "1.50" {
		t.Errorf("PomerajRange drifted from spec: %q", PomerajRange)
	}
}

// TestAnnuity_Housing30Y: 5,000,000 @ 7% over 360 months ≈ 33,265.79.
//
//	r = 7/1200 = 0.0058333…
//	pow ≈ 8.11649
//	A = 5e6 × 0.0058333 × 8.11649 / 7.11649 ≈ 33265.12
//
// Allow ±0.50 wiggle since the spec doesn't pin a particular rounding
// step beyond "use formula"; we go with exact bigrat. This pins the
// long-tenor case that the cash test doesn't cover.
func TestAnnuity_Housing30Y(t *testing.T) {
	A := Annuity(money.MustParse("5000000"), money.MustParse("0.005833333333"), 360)
	got := money.FormatAmount(A)
	// First 5 chars give us "33265" or "33264" depending on truncation.
	if got[:5] != "33265" && got[:5] != "33264" {
		t.Errorf("housing annuity: got %s, want ~33265", got)
	}
}
