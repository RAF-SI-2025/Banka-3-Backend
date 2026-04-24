package trading

import (
	"testing"
	"time"
)

func TestFillPricePerUnit(t *testing.T) {
	l := Listing{AskPrice: 12500, BidPrice: 12400}
	if got := fillPricePerUnit(DirectionBuy, l); got != 12500 {
		t.Errorf("buy: got %d, want ask 12500", got)
	}
	if got := fillPricePerUnit(DirectionSell, l); got != 12400 {
		t.Errorf("sell: got %d, want bid 12400", got)
	}
}

func TestRandomChunkBounds(t *testing.T) {
	cases := []struct {
		name      string
		remaining int64
	}{
		{"one", 1},
		{"small", 5},
		{"large", 1000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for i := 0; i < 200; i++ {
				got := randomChunk(c.remaining)
				if got < 1 || got > c.remaining {
					t.Fatalf("chunk %d out of [1,%d]", got, c.remaining)
				}
			}
		})
	}
}

func TestRandomChunkZero(t *testing.T) {
	// Not reachable from the executor (it skips is_done/remaining<=0 rows),
	// but we still want a deterministic answer: zero remaining means zero
	// chunk, no panic from rand.Int63n(0).
	if got := randomChunk(0); got != 0 {
		t.Errorf("chunk on 0 remaining: got %d, want 0", got)
	}
}

func TestNextDelayVolumeZeroUsesDefault(t *testing.T) {
	// Volume=0 happens on the very first fill of the day. Formula would
	// divide by zero; we fall back to executorDefaultDelaySeconds so the
	// first fill lands promptly and seeds volume for later ticks.
	for i := 0; i < 100; i++ {
		d := nextDelay(10, 0, false)
		if d < 0 || d > time.Duration(executorDefaultDelaySeconds)*time.Second {
			t.Fatalf("delay %s outside [0, %ds]", d, executorDefaultDelaySeconds)
		}
	}
}

func TestNextDelayFormula(t *testing.T) {
	// remaining=100, volume=1440 → max = 1440*100/1440 = 100 seconds.
	max := 100 * time.Second
	for i := 0; i < 200; i++ {
		d := nextDelay(100, 1440, false)
		if d < 0 || d > max {
			t.Fatalf("delay %s outside [0, %s]", d, max)
		}
	}
}

func TestNextDelayAfterHoursBonus(t *testing.T) {
	// After-hours: every computed delay gets +30 min.
	for i := 0; i < 100; i++ {
		d := nextDelay(100, 1440, true)
		if d < afterHoursDelayBonus {
			t.Fatalf("after-hours delay %s below bonus floor %s", d, afterHoursDelayBonus)
		}
		if d > afterHoursDelayBonus+100*time.Second {
			t.Fatalf("after-hours delay %s above ceiling", d)
		}
	}
}

func TestNextDelayHighVolumeClampsToOneSecond(t *testing.T) {
	// Volume >> remaining * 1440 → integer division yields 0. The helper
	// bumps the max to 1s so rand.Int63n doesn't panic on zero.
	for i := 0; i < 100; i++ {
		d := nextDelay(1, 1_000_000_000, false)
		if d < 0 || d > time.Second {
			t.Fatalf("delay %s outside [0, 1s]", d)
		}
	}
}

func TestFillPriceForOrderMarket(t *testing.T) {
	l := Listing{AskPrice: 12500, BidPrice: 12400}
	buy := &Order{OrderType: OrderMarket, Direction: DirectionBuy}
	sell := &Order{OrderType: OrderMarket, Direction: DirectionSell}
	if p, ok := fillPriceForOrder(buy, l); !ok || p != 12500 {
		t.Errorf("buy market: got %d/%v, want 12500/true", p, ok)
	}
	if p, ok := fillPriceForOrder(sell, l); !ok || p != 12400 {
		t.Errorf("sell market: got %d/%v, want 12400/true", p, ok)
	}
}

func TestFillPriceForOrderLimit(t *testing.T) {
	// Buy limit at 12600: ask 12500 is favorable (fill at ask); ask 12700 is
	// unfavorable (skip). Sell limit at 12500: bid 12600 favorable (fill at
	// bid); bid 12400 unfavorable.
	buy := &Order{OrderType: OrderLimit, Direction: DirectionBuy, PricePerUnit: 12600}
	if p, ok := fillPriceForOrder(buy, Listing{AskPrice: 12500, BidPrice: 12400}); !ok || p != 12500 {
		t.Errorf("buy favorable: got %d/%v, want 12500/true", p, ok)
	}
	if _, ok := fillPriceForOrder(buy, Listing{AskPrice: 12700, BidPrice: 12600}); ok {
		t.Errorf("buy unfavorable should skip")
	}

	sell := &Order{OrderType: OrderLimit, Direction: DirectionSell, PricePerUnit: 12500}
	if p, ok := fillPriceForOrder(sell, Listing{AskPrice: 12700, BidPrice: 12600}); !ok || p != 12600 {
		t.Errorf("sell favorable: got %d/%v, want 12600/true", p, ok)
	}
	if _, ok := fillPriceForOrder(sell, Listing{AskPrice: 12500, BidPrice: 12400}); ok {
		t.Errorf("sell unfavorable should skip")
	}
}

func TestFillPriceForOrderStopLimitBehavesAsLimit(t *testing.T) {
	// Once triggered_at is set, stop_limit should reuse the limit path.
	ttime := time.Now()
	o := &Order{OrderType: OrderStopLimit, Direction: DirectionBuy, PricePerUnit: 12600, StopPrice: 12500, TriggeredAt: &ttime}
	if p, ok := fillPriceForOrder(o, Listing{AskPrice: 12550, BidPrice: 12540}); !ok || p != 12550 {
		t.Errorf("stop_limit favorable: got %d/%v, want 12550/true", p, ok)
	}
	if _, ok := fillPriceForOrder(o, Listing{AskPrice: 12700, BidPrice: 12690}); ok {
		t.Errorf("stop_limit unfavorable should skip")
	}
}

func TestNeedsActivation(t *testing.T) {
	cases := []struct {
		name string
		o    *Order
		want bool
	}{
		{"market", &Order{OrderType: OrderMarket}, false},
		{"limit", &Order{OrderType: OrderLimit}, false},
		{"stop untriggered", &Order{OrderType: OrderStop}, true},
		{"stop_limit untriggered", &Order{OrderType: OrderStopLimit}, true},
		{"stop already triggered", &Order{OrderType: OrderStop, TriggeredAt: ptrTime(time.Now())}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := needsActivation(c.o); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestChooseChunkAON(t *testing.T) {
	// AON orders must commit the full remaining quantity every attempt so
	// the executor never emits a partial fill.
	o := &Order{RemainingPortions: 7, AllOrNone: true}
	for i := 0; i < 20; i++ {
		if got := chooseChunk(o); got != 7 {
			t.Fatalf("AON chunk: got %d, want 7", got)
		}
	}
}

func TestChooseChunkNonAON(t *testing.T) {
	// Non-AON falls back to the standard randomChunk, so chunk stays in
	// [1, remaining].
	o := &Order{RemainingPortions: 10, AllOrNone: false}
	for i := 0; i < 50; i++ {
		got := chooseChunk(o)
		if got < 1 || got > 10 {
			t.Fatalf("chunk %d out of range", got)
		}
	}
}

func TestStopTrigger(t *testing.T) {
	// Stop stores trigger in price_per_unit (legacy), stop_limit in stop_price.
	if got := stopTrigger(&Order{OrderType: OrderStop, PricePerUnit: 150}); got != 150 {
		t.Errorf("stop: got %d, want 150", got)
	}
	if got := stopTrigger(&Order{OrderType: OrderStopLimit, PricePerUnit: 155, StopPrice: 150}); got != 150 {
		t.Errorf("stop_limit: got %d, want 150", got)
	}
	if got := stopTrigger(&Order{OrderType: OrderMarket}); got != 0 {
		t.Errorf("market: got %d, want 0", got)
	}
}

func TestPlanSettlementSameCurrency(t *testing.T) {
	// Same-currency orders leave rate unset and copy the instrument-
	// currency cost straight through.
	s := &Server{}
	got, err := s.planSettlement("USD", "USD", 12500, DirectionBuy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.accAmount != 12500 || got.feeInstr != 12500 {
		t.Errorf("accAmount=%d feeInstr=%d, want 12500/12500", got.accAmount, got.feeInstr)
	}
	if got.fxRate != nil {
		t.Errorf("fxRate should be nil for same-currency, got %v", *got.fxRate)
	}
}
