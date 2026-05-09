package service

import (
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// TestDefaultMaintenanceFee pins the per-spec fee table.
//
// Spec p.12 explicitly shows 255 RSD for a standard RSD checking
// account. The FX example on p.13 has no "Održavanje" row, so FX
// accounts are fee-free. The other subtypes follow common Serbian
// banking practice (student/youth/unemployed waved; pensioner reduced;
// business higher).
func TestDefaultMaintenanceFee(t *testing.T) {
	cases := []struct {
		name     string
		kind     domain.AccountKind
		subtype  domain.AccountSubtype
		currency domain.Currency
		want     string
	}{
		{"personal RSD standard", domain.KindPersonalCheckingRSD, domain.SubtypeStandard, domain.CurrencyRSD, "255"},
		{"personal RSD student", domain.KindPersonalCheckingRSD, domain.SubtypeStudent, domain.CurrencyRSD, "0"},
		{"personal RSD youth", domain.KindPersonalCheckingRSD, domain.SubtypeYouth, domain.CurrencyRSD, "0"},
		{"personal RSD pensioner", domain.KindPersonalCheckingRSD, domain.SubtypePensioner, domain.CurrencyRSD, "100"},
		{"personal RSD savings", domain.KindPersonalCheckingRSD, domain.SubtypeSavings, domain.CurrencyRSD, "0"},
		{"personal FX EUR", domain.KindPersonalFX, domain.SubtypeUnspecified, domain.CurrencyEUR, "0"},
		{"business RSD DOO", domain.KindBusinessCheckingRSD, domain.SubtypeDOO, domain.CurrencyRSD, "500"},
		{"business RSD AD", domain.KindBusinessCheckingRSD, domain.SubtypeAD, domain.CurrencyRSD, "800"},
		{"business RSD foundation", domain.KindBusinessCheckingRSD, domain.SubtypeFoundation, domain.CurrencyRSD, "200"},
		{"business FX USD", domain.KindBusinessFX, domain.SubtypeUnspecified, domain.CurrencyUSD, "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DefaultMaintenanceFee(tc.kind, tc.subtype, tc.currency)
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// TestDefaultLimits pins the spec p.12-13 starter limits.
func TestDefaultLimits(t *testing.T) {
	if DefaultDailyLimit(domain.CurrencyRSD) != "250000" {
		t.Errorf("RSD daily: %s", DefaultDailyLimit(domain.CurrencyRSD))
	}
	if DefaultMonthlyLimit(domain.CurrencyRSD) != "1000000" {
		t.Errorf("RSD monthly: %s", DefaultMonthlyLimit(domain.CurrencyRSD))
	}
	if DefaultDailyLimit(domain.CurrencyEUR) != "5000" {
		t.Errorf("EUR daily: %s", DefaultDailyLimit(domain.CurrencyEUR))
	}
}
