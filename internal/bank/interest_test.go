package bank

import (
	"testing"
)

func TestBaseAnnualRate(t *testing.T) {
	tests := []struct {
		amount   float64
		expected float64
	}{
		{0, 6.25},
		{500_000.00, 6.25},
		{500_000.01, 6.00},
		{1_000_000.00, 6.00},
		{1_000_000.01, 5.75},
		{2_000_000.00, 5.75},
		{2_000_000.01, 5.50},
		{5_000_000.00, 5.50},
		{5_000_000.01, 5.25},
		{10_000_000.00, 5.25},
		{10_000_000.01, 5.00},
		{20_000_000.00, 5.00},
		{20_000_000.01, 4.75},
		{100_000_000.00, 4.75},
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
	// 1,000,000 RSD at 8% for 12 months => ~86,988.43
	got := CalculateAnnuity(1_000_000.00, 8.0, 12)
	expected := 86_988.43
	if got < expected-0.01 || got > expected+0.01 {
		t.Errorf("CalculateAnnuity(1M paras, 8%%, 12) = %v, want ~%v", got, expected)
	}

	// 10,000 RSD at 8% for 12 months => ~869.88
	got = CalculateAnnuity(10_000.00, 8.0, 12)
	expected = 869.88
	if got < expected-0.01 || got > expected+0.01 {
		t.Errorf("CalculateAnnuity(10000 paras, 8%%, 12) = %v, want ~%v", got, expected)
	}
}

func TestCalculateAnnuity_ZeroRate(t *testing.T) {
	got := CalculateAnnuity(12_000.00, 0, 12)
	if got != 1_000.00 {
		t.Errorf("CalculateAnnuity(12000.00, 0, 12) = %v, want %v", got, 1_000.00)
	}
}

func TestCalculateAnnuity_ZeroMonths(t *testing.T) {
	got := CalculateAnnuity(10_000.00, 5.0, 0)
	if got != 0 {
		t.Errorf("CalculateAnnuity(10000.00, 5, 0) = %v, want 0", got)
	}
}
