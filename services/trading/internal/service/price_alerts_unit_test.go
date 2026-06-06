package service

import (
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// TestPriceAlertCrossed pins the threshold-crossing semantics behind the
// todoSpec C3 scenarios: S27 (ABOVE-200 fires at 201), S28 (BELOW-300
// fires at/under 300), S29 (ABOVE-200 does NOT fire at 198).
func TestPriceAlertCrossed(t *testing.T) {
	cases := []struct {
		name      string
		cond      domain.PriceAlertCondition
		price     string
		threshold string
		want      bool
	}{
		{"S27 above fires at 201", domain.PriceAlertAbove, "201", "200", true},
		{"S27 below threshold no fire", domain.PriceAlertAbove, "195", "200", false},
		{"above fires exactly at threshold", domain.PriceAlertAbove, "200", "200", true},
		{"S29 above-200 at 198 no fire", domain.PriceAlertAbove, "198", "200", false},
		{"S28 below fires under 300", domain.PriceAlertBelow, "299", "300", true},
		{"below fires exactly at threshold", domain.PriceAlertBelow, "300", "300", true},
		{"below above threshold no fire", domain.PriceAlertBelow, "301", "300", false},
		{"unknown condition never fires", domain.PriceAlertCondition("X"), "999", "1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			price := money.MustParse(tc.price)
			threshold := money.MustParse(tc.threshold)
			if got := priceAlertCrossed(tc.cond, price, threshold); got != tc.want {
				t.Fatalf("priceAlertCrossed(%s, %s, %s) = %v, want %v",
					tc.cond, tc.price, tc.threshold, got, tc.want)
			}
		})
	}
}

func TestPriceAlertConditionValid(t *testing.T) {
	if !domain.PriceAlertAbove.Valid() || !domain.PriceAlertBelow.Valid() {
		t.Fatal("ABOVE/BELOW must be valid")
	}
	if domain.PriceAlertCondition("").Valid() || domain.PriceAlertCondition("MAYBE").Valid() {
		t.Fatal("empty/unknown must be invalid")
	}
}
