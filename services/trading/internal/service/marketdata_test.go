package service

import (
	"math"
	"strconv"
	"testing"
)

func TestStockSpread_SymmetricAround(t *testing.T) {
	bid, ask, err := stockSpread("100.00", 0.001)
	if err != nil {
		t.Fatalf("stockSpread: %v", err)
	}
	if bid != "99.9000" || ask != "100.1000" {
		t.Errorf("bid/ask = %s/%s, want 99.9000/100.1000", bid, ask)
	}
}

func TestStockSpread_NegativeClampedToZero(t *testing.T) {
	bid, ask, err := stockSpread("100.00", -0.005)
	if err != nil {
		t.Fatalf("stockSpread: %v", err)
	}
	if bid != ask {
		t.Errorf("bid/ask = %s/%s, expected equal under zero spread", bid, ask)
	}
}

func TestStockSpread_RejectsGarbage(t *testing.T) {
	if _, _, err := stockSpread("not-a-number", 0.001); err == nil {
		t.Fatalf("expected error from garbage input")
	}
}

func TestMidpoint_AveragesBidAsk(t *testing.T) {
	mid, err := midpoint("1.0840", "1.0860")
	if err != nil {
		t.Fatalf("midpoint: %v", err)
	}
	got, _ := strconv.ParseFloat(mid, 64)
	if math.Abs(got-1.0850) > 1e-6 {
		t.Errorf("mid = %s, want 1.0850", mid)
	}
}

func TestChangeAmt_PriceMovedUp(t *testing.T) {
	got, err := changeAmt("450.00", "454.30")
	if err != nil {
		t.Fatalf("changeAmt: %v", err)
	}
	if got != "4.3000" {
		t.Errorf("change = %q, want 4.3000", got)
	}
}

func TestChangeAmt_PriceMovedDown(t *testing.T) {
	got, err := changeAmt("100.00", "95.00")
	if err != nil {
		t.Fatalf("changeAmt: %v", err)
	}
	if got != "-5.0000" {
		t.Errorf("change = %q, want -5.0000", got)
	}
}

func TestChangeAmt_EmptyPrev(t *testing.T) {
	got, err := changeAmt("", "100.00")
	if err != nil {
		t.Fatalf("changeAmt: %v", err)
	}
	if got != "0" {
		t.Errorf("change = %q, want 0 (no prev)", got)
	}
}
