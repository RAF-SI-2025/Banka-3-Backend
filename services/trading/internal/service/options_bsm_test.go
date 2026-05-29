package service

import (
	"math"
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// TestBlackScholes_KnownValues pins the Black-Scholes implementation
// against textbook reference values. Inputs from Hull "Options,
// Futures, and Other Derivatives" 9th ed. Ch. 15 Example 15.6 (S=42,
// K=40, r=10%, σ=20%, T=0.5) where the call price is 4.7594 and the
// put 0.8086 to four decimals.
func TestBlackScholes_KnownValues(t *testing.T) {
	const (
		s     = 42.0
		k     = 40.0
		r     = 0.10
		sigma = 0.20
		T     = 0.5
	)
	got := blackScholes(domain.OptionCall, s, k, r, sigma, T)
	if math.Abs(got-4.7594) > 0.001 {
		t.Errorf("call price = %.4f, want 4.7594", got)
	}
	got = blackScholes(domain.OptionPut, s, k, r, sigma, T)
	if math.Abs(got-0.8086) > 0.001 {
		t.Errorf("put price = %.4f, want 0.8086", got)
	}
}

// TestBlackScholes_DeepITMCall checks the limiting case: a deep
// in-the-money call with no time value should be worth ~ S - K·e^(-rT).
func TestBlackScholes_DeepITMCall(t *testing.T) {
	got := blackScholes(domain.OptionCall, 100, 50, 0.05, 0.20, 0.0001)
	intrinsic := 100.0 - 50.0
	if math.Abs(got-intrinsic) > 0.05 {
		t.Errorf("deep ITM call = %.4f, want close to %.4f", got, intrinsic)
	}
}

// TestBlackScholes_AtExpiry returns intrinsic value (max(S-K,0) for
// calls; max(K-S,0) for puts).
func TestBlackScholes_AtExpiry(t *testing.T) {
	cases := []struct {
		side     domain.OptionType
		s, k     float64
		expected float64
	}{
		{domain.OptionCall, 110, 100, 10},
		{domain.OptionCall, 90, 100, 0},
		{domain.OptionPut, 110, 100, 0},
		{domain.OptionPut, 90, 100, 10},
	}
	for _, c := range cases {
		got := blackScholes(c.side, c.s, c.k, 0.05, 0.20, 0)
		if math.Abs(got-c.expected) > 1e-9 {
			t.Errorf("%s S=%.0f K=%.0f T=0: got %.4f, want %.4f", c.side, c.s, c.k, got, c.expected)
		}
	}
}

// TestStrikeGrid_SpecExample pins the spec p.43 worked example: ATM
// 112 produces [107..117].
func TestStrikeGrid_SpecExample(t *testing.T) {
	got := strikeGrid(112)
	want := []float64{107, 108, 109, 110, 111, 112, 113, 114, 115, 116, 117}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("strike[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestStrikeGrid_FiltersNonPositive: a stock priced at 3 should not
// emit strikes ≤ 0.
func TestStrikeGrid_FiltersNonPositive(t *testing.T) {
	got := strikeGrid(3)
	for _, k := range got {
		if k <= 0 {
			t.Fatalf("got non-positive strike %v", k)
		}
	}
	// Expect 8 strikes (3-2..3+5 = 1..8).
	if len(got) != 8 {
		t.Errorf("len = %d, want 8 (filtering ≤0)", len(got))
	}
}

// TestExpiryLadder_SpecLayout pins: 12 expiries, first six at +6d
// intervals, next six at +30d intervals, anchored to today's date in
// the configured location.
func TestExpiryLadder_SpecLayout(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Belgrade")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	pinned := time.Date(2026, 5, 10, 12, 0, 0, 0, loc)
	g := &OptionGenerator{Now: func() time.Time { return pinned }, Belgrade: loc}

	out := g.expiryLadder()
	if len(out) != 12 {
		t.Fatalf("len = %d, want 12", len(out))
	}
	wantOffsets := []int{6, 12, 18, 24, 30, 36, 66, 96, 126, 156, 186, 216}
	today := time.Date(2026, 5, 10, 0, 0, 0, 0, loc)
	for i, off := range wantOffsets {
		want := today.AddDate(0, 0, off)
		if !out[i].Equal(want) {
			t.Errorf("expiry[%d] = %v, want %v (offset +%dd)", i, out[i], want, off)
		}
	}
	// Spec p.43: last - first = 30 days for the first phase (rungs 0..5).
	if d := out[5].Sub(out[0]); d != 30*24*time.Hour {
		t.Errorf("first-phase span = %v, want 30 days", d)
	}
}

func TestOptionTicker_DistinguishesAxes(t *testing.T) {
	expiry := time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)
	a := optionTicker("AAPL", expiry, domain.OptionCall, 190)
	b := optionTicker("AAPL", expiry, domain.OptionPut, 190)
	c := optionTicker("AAPL", expiry, domain.OptionCall, 200)
	d := optionTicker("AAPL", expiry.AddDate(0, 0, 7), domain.OptionCall, 190)
	for _, pair := range [][2]string{{a, b}, {a, c}, {a, d}, {b, c}, {b, d}, {c, d}} {
		if pair[0] == pair[1] {
			t.Errorf("colliding tickers: %s and %s", pair[0], pair[1])
		}
	}
	if a != "AAPL-260709-C-190" {
		t.Errorf("ticker = %q, want AAPL-260709-C-190", a)
	}
}
