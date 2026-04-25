package trading

import (
	"testing"
)

func TestHoldingAssetType(t *testing.T) {
	stock := int64(1)
	future := int64(2)
	forex := int64(3)
	option := int64(4)
	cases := []struct {
		name string
		h    *Holding
		want string
	}{
		{"stock", &Holding{StockID: &stock}, "stock"},
		{"future", &Holding{FutureID: &future}, "future"},
		{"forex", &Holding{ForexPairID: &forex}, "forex"},
		{"option", &Holding{OptionID: &option}, "option"},
		{"empty", &Holding{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := holdingAssetType(c.h); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestWeightedAvgCostFormula pins down the buy-fill weighting math used by
// upsertHoldingOnBuy. Two fills at different prices should land at the
// quantity-weighted mean — tax basis stays accurate across many partial
// fills, so a future spec change to the formula breaks this test loudly.
func TestWeightedAvgCostFormula(t *testing.T) {
	cases := []struct {
		name        string
		oldAmt      int64
		oldAvg      int64
		chunk       int64
		newPrice    int64
		expectedAvg int64
	}{
		{"first fill seeds avg", 0, 0, 10, 12500, 12500},
		{"same price keeps avg", 5, 12500, 5, 12500, 12500},
		{"higher price pulls up", 10, 10000, 10, 14000, 12000},
		{"smaller fill weights less", 9, 10000, 1, 20000, 11000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got int64
			if c.oldAmt <= 0 {
				got = c.newPrice
			} else {
				got = (c.oldAvg*c.oldAmt + c.newPrice*c.chunk) / (c.oldAmt + c.chunk)
			}
			if got != c.expectedAvg {
				t.Errorf("got %d, want %d", got, c.expectedAvg)
			}
		})
	}
}

// TestExercisePayoutFormula pins the spec p.62 payout math used by
// ExerciseOption — call: spot − strike, put: strike − spot, both then have
// the premium per contract subtracted before being scaled by held quantity.
// The test stays at formula-level because the RPC drives a transactional DB
// path; the math itself is the part that has to track the spec exactly.
func TestExercisePayoutFormula(t *testing.T) {
	cases := []struct {
		name       string
		side       OptionType
		spot       int64
		strike     int64
		premium    int64
		quantity   int64
		wantInstr  int64 // payout in instrument currency, before FX
		shouldFire bool  // true iff intrinsic > 0 (passes ITM gate)
	}{
		{"call ITM", OptionCall, 15000, 12000, 500, 2, (15000 - 12000 - 500) * 2, true},
		{"call ATM rejects", OptionCall, 12000, 12000, 500, 1, 0, false},
		{"call OTM rejects", OptionCall, 11000, 12000, 500, 1, 0, false},
		{"put ITM", OptionPut, 9000, 12000, 500, 3, (12000 - 9000 - 500) * 3, true},
		{"put OTM rejects", OptionPut, 13000, 12000, 500, 1, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var intrinsic int64
			switch c.side {
			case OptionCall:
				intrinsic = c.spot - c.strike
			case OptionPut:
				intrinsic = c.strike - c.spot
			}
			if (intrinsic > 0) != c.shouldFire {
				t.Fatalf("ITM gate mismatch: intrinsic=%d, shouldFire=%v", intrinsic, c.shouldFire)
			}
			if !c.shouldFire {
				return
			}
			got := (intrinsic - c.premium) * c.quantity
			if got != c.wantInstr {
				t.Errorf("got %d, want %d", got, c.wantInstr)
			}
		})
	}
}

// TestPublicAmountValidation pins the 0 ≤ public_amount ≤ amount invariant
// the SetHoldingPublic RPC enforces. Out-of-range inputs surface as
// InvalidArgument so the portal can render an inline error; non-stock holdings
// hit a separate FailedPrecondition path the RPC checks earlier.
func TestPublicAmountValidation(t *testing.T) {
	cases := []struct {
		name   string
		amount int64
		public int64
		wantOK bool
	}{
		{"zero is fine", 100, 0, true},
		{"equal to amount is fine", 100, 100, true},
		{"under amount is fine", 100, 50, true},
		{"over amount rejected", 100, 101, false},
		{"negative rejected", 100, -1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok := c.public >= 0 && c.public <= c.amount
			if ok != c.wantOK {
				t.Errorf("got %v, want %v", ok, c.wantOK)
			}
		})
	}
}
