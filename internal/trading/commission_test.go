package trading

import "testing"

func TestComputeCommission(t *testing.T) {
	cases := []struct {
		name   string
		ot     OrderType
		approx int64
		want   int64
	}{
		// Market/stop: 14% of approx, capped at 700.
		{"market tiny → pct", OrderMarket, 1000, 140},
		{"market at cap", OrderMarket, 5000, 700},
		{"market above cap", OrderMarket, 100000, 700},
		{"stop follows market", OrderStop, 1000, 140},
		{"stop above cap", OrderStop, 100000, 700},

		// Limit/stop_limit: 24% of approx, capped at 1200.
		{"limit tiny → pct", OrderLimit, 1000, 240},
		{"limit at cap", OrderLimit, 5000, 1200},
		{"limit above cap", OrderLimit, 100000, 1200},
		{"stop_limit follows limit", OrderStopLimit, 1000, 240},
		{"stop_limit above cap", OrderStopLimit, 100000, 1200},

		{"zero approx → zero", OrderMarket, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := computeCommission(c.ot, c.approx); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
