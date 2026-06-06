package service

import (
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
)

// TestFundDividendSlice covers the S71 proportional split: client A with
// 30% of the units gets 30% of a 10.000 RSD dividend, B with 70% gets
// 70% — exactly the spec example.
func TestFundDividendSlice(t *testing.T) {
	rsd := money.MustParse("10000")
	total := money.MustParse("100") // 100 units total

	a := fundDividendSlice(rsd, money.MustParse("30"), total) // 30%
	if a == nil || a.FloatString(2) != "3000.00" {
		t.Fatalf("A slice = %v, want 3000.00", a)
	}
	b := fundDividendSlice(rsd, money.MustParse("70"), total) // 70%
	if b == nil || b.FloatString(2) != "7000.00" {
		t.Fatalf("B slice = %v, want 7000.00", b)
	}
	// Slices sum to the whole dividend (no leakage).
	sum := money.Add(a, b)
	if sum.FloatString(2) != "10000.00" {
		t.Fatalf("A+B = %v, want 10000.00", sum)
	}
}

func TestFundDividendSlice_NonPositive(t *testing.T) {
	rsd := money.MustParse("10000")
	if s := fundDividendSlice(rsd, money.MustParse("0"), money.MustParse("100")); s != nil {
		t.Fatalf("zero units must yield no slice, got %v", s)
	}
	if s := fundDividendSlice(rsd, money.MustParse("30"), money.MustParse("0")); s != nil {
		t.Fatalf("zero total units must yield no slice, got %v", s)
	}
	if s := fundDividendSlice(nil, money.MustParse("30"), money.MustParse("100")); s != nil {
		t.Fatalf("nil rsd must yield no slice, got %v", s)
	}
}

// TestFundDividendSlice_Fractional proves a non-round share still splits
// proportionally (the unit price absorbs the remainder through the
// fund's appreciation; the ledger records the exact RSD slice).
func TestFundDividendSlice_Fractional(t *testing.T) {
	rsd := money.MustParse("1000")
	total := money.MustParse("3")
	a := fundDividendSlice(rsd, money.MustParse("1"), total) // 1/3
	b := fundDividendSlice(rsd, money.MustParse("2"), total) // 2/3
	if a == nil || b == nil {
		t.Fatal("expected slices")
	}
	if got := money.Add(a, b).FloatString(4); got != "1000.0000" {
		t.Fatalf("1/3 + 2/3 of 1000 = %s, want 1000.0000", got)
	}
}

// TestReinvestQuantity covers the S70 whole-share floor: a dividend buys
// floor(gross / price) shares; too small a dividend buys nothing.
func TestReinvestQuantity(t *testing.T) {
	// 250 RSD dividend at price 100 → 2 whole shares (floor of 2.5).
	if q := reinvestQuantity(money.MustParse("250"), money.MustParse("100")); q != 2 {
		t.Fatalf("reinvestQuantity(250,100) = %d, want 2", q)
	}
	// Exactly affordable.
	if q := reinvestQuantity(money.MustParse("300"), money.MustParse("100")); q != 3 {
		t.Fatalf("reinvestQuantity(300,100) = %d, want 3", q)
	}
	// Dividend below one share's price → 0 (cash stays liquid).
	if q := reinvestQuantity(money.MustParse("50"), money.MustParse("100")); q != 0 {
		t.Fatalf("reinvestQuantity(50,100) = %d, want 0", q)
	}
	// Non-positive inputs.
	if q := reinvestQuantity(money.MustParse("250"), money.MustParse("0")); q != 0 {
		t.Fatalf("zero price must yield 0 qty, got %d", q)
	}
	if q := reinvestQuantity(nil, money.MustParse("100")); q != 0 {
		t.Fatalf("nil gross must yield 0 qty, got %d", q)
	}
}
