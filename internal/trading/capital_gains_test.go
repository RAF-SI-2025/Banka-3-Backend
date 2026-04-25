package trading

import (
	"testing"
	"time"
)

// TestCapitalGainsTaxFormula pins the spec p.63 example ("100 RSD buy, 250
// RSD sell → 150 RSD dobit → 22.5 RSD porez") plus the cross-currency and
// loss cases. Rounding is intentionally up on the account-currency tax side
// so rounding never under-taxes the placer.
func TestCapitalGainsTaxFormula(t *testing.T) {
	cases := []struct {
		name       string
		profit     int64
		rateAccRSD float64
		wantAcc    int64
		wantRSD    int64
	}{
		{"spec example RSD", 15000, 1.0, 2250, 2250},
		{"loss zero", -500, 1.0, 0, 0},
		{"zero profit skipped", 0, 1.0, 0, 0},
		{"rounds up on account", 7, 1.0, 2, 2}, // 7 * 0.15 = 1.05 → rounds up to 2
		{"EUR @ 117.15", 10000, 117.15, 1500, 175725},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			acc, rsd := computeCapitalGainsTax(c.profit, c.rateAccRSD)
			if acc != c.wantAcc {
				t.Errorf("acc tax: got %d, want %d", acc, c.wantAcc)
			}
			if rsd != c.wantRSD {
				t.Errorf("RSD tax: got %d, want %d", rsd, c.wantRSD)
			}
		})
	}
}

// TestCapitalGainsPeriodMonthly confirms the period key collapses any moment
// in the month to a stable YYYY-MM bucket — the next-issue collection cron
// joins on this column, so a drift from UTC/local or a different separator
// would split the same month into two pay-outs.
func TestCapitalGainsPeriodMonthly(t *testing.T) {
	mid := time.Date(2026, 3, 14, 15, 9, 26, 0, time.UTC)
	if got := capitalGainsPeriod(mid); got != "2026-03" {
		t.Errorf("got %q, want 2026-03", got)
	}
	endOfMonth := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	if got := capitalGainsPeriod(endOfMonth); got != "2026-12" {
		t.Errorf("got %q, want 2026-12", got)
	}
}
