package service

import (
	"math/big"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

func TestComputeMaintenanceMargin(t *testing.T) {
	// stock: 50% × price (no listing.contract_size factor for stocks per spec p.46)
	stock := &domain.Security{Type: domain.SecurityStock}
	listing := &domain.Listing{Price: "100", ContractSize: "1"}
	mm, ok := computeMaintenanceMargin(stock, listing)
	if !ok || mm.Cmp(big.NewRat(50, 1)) != 0 {
		t.Fatalf("stock margin: ok=%v mm=%s", ok, money.FormatAmount(mm))
	}

	// future: 10% × contract_size × price
	fut := &domain.Security{Type: domain.SecurityFuture}
	mm, ok = computeMaintenanceMargin(fut, &domain.Listing{Price: "50", ContractSize: "1000"})
	if !ok || mm.Cmp(big.NewRat(5000, 1)) != 0 {
		t.Fatalf("future margin: ok=%v mm=%s", ok, money.FormatAmount(mm))
	}

	// option: 50% × 100 × premium (spec p.48; we use premium as the
	// proxy in the listing-less path)
	opt := &domain.Security{Type: domain.SecurityOption, Premium: "5"}
	mm, ok = computeMaintenanceMargin(opt, nil)
	if !ok || mm.Cmp(big.NewRat(250, 1)) != 0 {
		t.Fatalf("option margin: ok=%v mm=%s", ok, money.FormatAmount(mm))
	}

	// missing listing for non-option types → not computable
	if _, ok := computeMaintenanceMargin(fut, nil); ok {
		t.Fatal("future margin should be unavailable without a listing")
	}
}

func TestFilterStrikeWindow(t *testing.T) {
	mk := func(strike string) *OptionChainRow { return &OptionChainRow{StrikePrice: strike} }
	rows := []*OptionChainRow{
		mk("105"), mk("106"), mk("107"), mk("108"), mk("109"),
		mk("110"), mk("111"), mk("112"), mk("113"), mk("114"),
	}
	shared, _ := money.Parse("110")
	got := filterStrikeWindow(rows, shared, 2)
	// Expect 2 below + at-the-money + 2 above = 5 rows: 108,109,110,111,112
	wantStrikes := []string{"108", "109", "110", "111", "112"}
	if len(got) != len(wantStrikes) {
		t.Fatalf("got %d rows, want %d (%v)", len(got), len(wantStrikes), got)
	}
	for i, r := range got {
		if r.StrikePrice != wantStrikes[i] {
			t.Errorf("[%d] got %q want %q", i, r.StrikePrice, wantStrikes[i])
		}
	}
}

func TestValidateSecurity(t *testing.T) {
	good := domain.Security{Ticker: "MSFT", Name: "Microsoft", Type: domain.SecurityStock, Currency: domain.CurrencyUSD, OutstandingShares: 1000}
	if err := validateSecurity(&good); err != nil {
		t.Fatalf("good stock: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*domain.Security)
	}{
		{"empty ticker", func(s *domain.Security) { s.Ticker = "" }},
		{"unsupported currency", func(s *domain.Security) { s.Currency = "XYZ" }},
		{"stock without shares", func(s *domain.Security) { s.OutstandingShares = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := good
			tc.mut(&s)
			if err := validateSecurity(&s); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	// Forex with same base/quote should fail
	forex := domain.Security{
		Ticker:        "EUREUR",
		Name:          "Bad",
		Type:          domain.SecurityForex,
		Currency:      domain.CurrencyEUR,
		BaseCurrency:  domain.CurrencyEUR,
		QuoteCurrency: domain.CurrencyEUR,
	}
	if err := validateSecurity(&forex); err == nil {
		t.Fatal("expected forex same-base error")
	}

	// Forex without contract_size should be auto-defaulted to "1000"
	forex.QuoteCurrency = domain.CurrencyUSD
	if err := validateSecurity(&forex); err != nil {
		t.Fatalf("good forex: %v", err)
	}
	if forex.ContractSize != "1000" {
		t.Errorf("expected default contract size 1000, got %q", forex.ContractSize)
	}
}
