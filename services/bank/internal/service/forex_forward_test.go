package service

import (
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/schedule"
)

// TestComputeForwardRate pins the spec forward-rate formula with a worked
// example:
//
//	ForwardRate = SpotAsk × (1 + (days/365) × spread)
//
// SpotAsk = 117.50 RSD/EUR, spread = 2%/yr, days = 365 ⇒
//
//	117.50 × (1 + 1 × 0.02) = 117.50 × 1.02 = 119.85
func TestComputeForwardRate(t *testing.T) {
	tests := []struct {
		name    string
		spotAsk string
		spread  string
		days    int
		want    string // FormatRate (8 dp)
	}{
		{"one year 2pct", "117.50", "0.02", 365, "119.85000000"},
		// Half a year at 2%/yr: 117.50 × (1 + 0.5 × 0.02) = 117.50 × 1.01 = 118.675
		{"half year 2pct", "117.50", "0.02", 182, "118.67178082"},
		// Zero spread leaves the spot rate untouched.
		{"zero spread", "100.00", "0", 90, "100.00000000"},
		// 73 days (= 1/5 year) at 5%/yr: 100 × (1 + 0.2 × 0.05) = 100 × 1.01 = 101.
		{"one fifth year 5pct", "100.00", "0.05", 73, "101.00000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := money.FormatRate(computeForwardRate(money.MustParse(tt.spotAsk), money.MustParse(tt.spread), tt.days))
			if got != tt.want {
				t.Fatalf("computeForwardRate(%s, %s, %d) = %s, want %s", tt.spotAsk, tt.spread, tt.days, got, tt.want)
			}
		})
	}
}

func TestComputeForwardRateWorkedQuoteAmount(t *testing.T) {
	// Worked end-to-end: buy forward 1000 EUR, spot ASK 117.50, 2%/yr,
	// one year out. ForwardRate = 119.85, so the RSD obligation is
	// 119.85 × 1000 = 119850.0000.
	rate := computeForwardRate(money.MustParse("117.50"), money.MustParse("0.02"), 365)
	quoteAmount := money.Mul(rate, money.MustParse("1000"))
	if got := money.FormatAmount(quoteAmount); got != "119850.0000" {
		t.Fatalf("quote amount = %s, want 119850.0000", got)
	}
	// Commission at the default 0.5% of the obligation = 599.25 RSD.
	commission := money.Mul(quoteAmount, money.MustParse("0.005"))
	if got := money.FormatAmount(commission); got != "599.2500" {
		t.Fatalf("commission = %s, want 599.2500", got)
	}
}

func TestDaysToSettlement(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		settlement time.Time
		want       int
	}{
		{"exactly 365 days", now.AddDate(1, 0, 0), 365},
		{"tomorrow same time", now.AddDate(0, 0, 1), 1},
		// A few hours into the future rounds up to a full day.
		{"hours ahead rounds up", now.Add(3 * time.Hour), 1},
		{"30 days exact", now.AddDate(0, 0, 30), 30},
		// 30 days plus a bit rounds up to 31.
		{"30 days plus hours", now.AddDate(0, 0, 30).Add(5 * time.Hour), 31},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := daysToSettlement(now, tt.settlement); got != tt.want {
				t.Fatalf("daysToSettlement = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestValidateFutureSettlementGuard(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	// Past and present dates must be rejected (reused pkg/schedule guard).
	if err := schedule.ValidateFuture(now, now); err == nil {
		t.Fatal("expected now == settlement to be rejected")
	}
	if err := schedule.ValidateFuture(now.AddDate(0, 0, -1), now); err == nil {
		t.Fatal("expected past settlement to be rejected")
	}
	if err := schedule.ValidateFuture(now.AddDate(0, 0, 1), now); err != nil {
		t.Fatalf("expected future settlement to pass, got %v", err)
	}
}
