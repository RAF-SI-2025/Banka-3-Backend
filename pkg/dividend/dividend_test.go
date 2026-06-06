package dividend

import (
	"math/big"
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
)

func rat(s string) *big.Rat { return money.MustParse(s) }

func TestQuarterly(t *testing.T) {
	cases := []struct {
		name  string
		qty   int64
		price string
		yield string
		want  string
	}{
		// Spec S54: 50 × 200 × (0.005/4) = 12.50.
		{"spec S54", 50, "200", "0.005", "12.5"},
		{"one share", 1, "100", "0.04", "1"}, // 100×0.04/4 = 1
		{"zero qty", 0, "200", "0.005", "0"},
		{"zero yield", 50, "200", "0", "0"},
		{"zero price", 50, "0", "0.005", "0"},
		{"negative qty", -5, "200", "0.005", "0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Quarterly(c.qty, rat(c.price), rat(c.yield))
			if got.Cmp(rat(c.want)) != 0 {
				t.Errorf("Quarterly(%d, %s, %s) = %s; want %s",
					c.qty, c.price, c.yield, money.FormatAmount(got), c.want)
			}
		})
	}
}

func TestQuarterlyNilSafe(t *testing.T) {
	if Quarterly(10, nil, rat("0.01")).Sign() != 0 {
		t.Error("nil price should yield zero")
	}
	if Quarterly(10, rat("100"), nil).Sign() != 0 {
		t.Error("nil yield should yield zero")
	}
}

func TestTax(t *testing.T) {
	if got := Tax(rat("100")); got.Cmp(rat("15")) != 0 {
		t.Errorf("Tax(100) = %s; want 15", money.FormatAmount(got))
	}
	if got := Tax(rat("12.5")); got.Cmp(rat("1.875")) != 0 {
		t.Errorf("Tax(12.5) = %s; want 1.875", money.FormatAmount(got))
	}
	if Tax(rat("0")).Sign() != 0 {
		t.Error("Tax(0) should be 0")
	}
	if Tax(nil).Sign() != 0 {
		t.Error("Tax(nil) should be 0")
	}
}

func TestQuarterOf(t *testing.T) {
	cases := map[time.Month]int{
		time.January: 1, time.March: 1,
		time.April: 2, time.June: 2,
		time.July: 3, time.September: 3,
		time.October: 4, time.December: 4,
	}
	for m, want := range cases {
		got := QuarterOf(time.Date(2026, m, 15, 0, 0, 0, 0, time.UTC))
		if got != want {
			t.Errorf("QuarterOf(%s) = %d; want %d", m, got, want)
		}
	}
}

func TestLastBusinessDayOfQuarter(t *testing.T) {
	utc := time.UTC
	cases := []struct {
		year, q int
		wantY   int
		wantM   time.Month
		wantD   int
	}{
		// 2026-03-31 is a Tuesday → itself.
		{2026, 1, 2026, time.March, 31},
		// 2026-12-31 is a Thursday → itself.
		{2026, 4, 2026, time.December, 31},
		// 2024-03-31 is a Sunday → roll back to Fri 2024-03-29.
		{2024, 1, 2024, time.March, 29},
		// 2024-06-30 is a Sunday → roll back to Fri 2024-06-28.
		{2024, 2, 2024, time.June, 28},
	}
	for _, c := range cases {
		got := LastBusinessDayOfQuarter(c.year, c.q, utc)
		y, m, d := got.Date()
		if y != c.wantY || m != c.wantM || d != c.wantD {
			t.Errorf("LastBusinessDayOfQuarter(%d, Q%d) = %04d-%02d-%02d; want %04d-%02d-%02d",
				c.year, c.q, y, m, d, c.wantY, c.wantM, c.wantD)
		}
		if wd := got.Weekday(); wd == time.Saturday || wd == time.Sunday {
			t.Errorf("result %s is a weekend (%s)", got.Format("2006-01-02"), wd)
		}
	}
}

func TestIsLastBusinessDayOfQuarter(t *testing.T) {
	utc := time.UTC
	// Friday 2024-06-28 is the last business day of Q2 2024.
	if !IsLastBusinessDayOfQuarter(time.Date(2024, 6, 28, 9, 0, 0, 0, utc)) {
		t.Error("2024-06-28 should be the last business day of Q2")
	}
	// The weekend quarter-end itself is not a business day.
	if IsLastBusinessDayOfQuarter(time.Date(2024, 6, 30, 9, 0, 0, 0, utc)) {
		t.Error("2024-06-30 (Sunday) is not a business day")
	}
	// A regular mid-quarter day is not it.
	if IsLastBusinessDayOfQuarter(time.Date(2024, 6, 27, 9, 0, 0, 0, utc)) {
		t.Error("2024-06-27 is not the last business day of Q2")
	}
}
