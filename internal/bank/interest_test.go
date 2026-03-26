package bank

import (
	"testing"
)

func TestBaseAnnualRate(t *testing.T) {
	tests := []struct {
		amount   int64
		expected float64
	}{
		{0, 6.25},
		{500_000_00, 6.25},
		{500_000_01, 6.00},
		{1_000_000_00, 6.00},
		{1_000_000_01, 5.75},
		{2_000_000_00, 5.75},
		{2_000_000_01, 5.50},
		{5_000_000_00, 5.50},
		{5_000_000_01, 5.25},
		{10_000_000_00, 5.25},
		{10_000_000_01, 5.00},
		{20_000_000_00, 5.00},
		{20_000_000_01, 4.75},
		{100_000_000_00, 4.75},
	}
	for _, tt := range tests {
		got := BaseAnnualRate(tt.amount)
		if got != tt.expected {
			t.Errorf("BaseAnnualRate(%v) = %v, want %v", tt.amount, got, tt.expected)
		}
	}
}

func TestMarginForLoanType(t *testing.T) {
	tests := []struct {
		lt       loan_type
		expected float64
	}{
		{Cash, 1.75},
		{Mortgage, 1.50},
		{Car, 1.25},
		{Refinancing, 1.00},
		{Student, 0.75},
	}
	for _, tt := range tests {
		got := MarginForLoanType(tt.lt)
		if got != tt.expected {
			t.Errorf("MarginForLoanType(%v) = %v, want %v", tt.lt, got, tt.expected)
		}
	}
}

func TestCalculateAnnuity(t *testing.T) {
	// 1,000,000 RSD (= 1_000_000_00 paras) at 8% for 12 months => ~8,698,843 paras
	got := CalculateAnnuity(1_000_000_00, 8.0, 12)
	expected := int64(8_698_843)
	if got < expected-1 || got > expected+1 {
		t.Errorf("CalculateAnnuity(1M paras, 8%%, 12) = %v, want ~%v", got, expected)
	}

	// 10,000 RSD (= 10_000_00 paras) at 8% for 12 months => ~86,988 paras
	got = CalculateAnnuity(10_000_00, 8.0, 12)
	expected = int64(86_988)
	if got < expected-1 || got > expected+1 {
		t.Errorf("CalculateAnnuity(10000 paras, 8%%, 12) = %v, want ~%v", got, expected)
	}
}

func TestCalculateAnnuity_ZeroRate(t *testing.T) {
	got := CalculateAnnuity(12_000_00, 0, 12)
	if got != 1_000_00 {
		t.Errorf("CalculateAnnuity(12000_00, 0, 12) = %v, want %v", got, 1_000_00)
	}
}

func TestCalculateAnnuity_ZeroMonths(t *testing.T) {
	got := CalculateAnnuity(10_000_00, 5.0, 0)
	if got != 0 {
		t.Errorf("CalculateAnnuity(10000_00, 5, 0) = %v, want 0", got)
	}
}
