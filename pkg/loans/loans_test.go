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
		{"500000", "6.25"},     // boundary belongs to first bracket
		{"500001", "6.00"},     // first cent into next bracket
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
