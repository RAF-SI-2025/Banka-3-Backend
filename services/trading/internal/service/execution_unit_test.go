package service

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

func TestStopTriggered(t *testing.T) {
	s := &Service{}
	cases := []struct {
		name      string
		typ       domain.OrderType
		direction domain.Direction
		stop      string
		ask, bid  string
		want      bool
	}{
		// STOP: spec p.52 strict comparison vs ask/bid.
		{"stop buy hits when ask > stop", domain.OrderStop, domain.DirectionBuy, "100", "101", "100", true},
		{"stop buy misses when ask == stop", domain.OrderStop, domain.DirectionBuy, "100", "100", "99", false},
		{"stop buy misses when ask < stop", domain.OrderStop, domain.DirectionBuy, "100", "99", "98", false},
		{"stop sell hits when bid < stop", domain.OrderStop, domain.DirectionSell, "100", "100", "99", true},
		{"stop sell misses when bid == stop", domain.OrderStop, domain.DirectionSell, "100", "101", "100", false},
		{"stop sell misses when bid > stop", domain.OrderStop, domain.DirectionSell, "100", "102", "101", false},
		// STOP_LIMIT: spec p.54 — buy is loose ("dostigne ili pređe"),
		// sell is strict ("padne ispod").
		{"stoplimit buy hits when ask == stop", domain.OrderStopLimit, domain.DirectionBuy, "100", "100", "99", true},
		{"stoplimit buy hits when ask > stop", domain.OrderStopLimit, domain.DirectionBuy, "100", "101", "100", true},
		{"stoplimit buy misses when ask < stop", domain.OrderStopLimit, domain.DirectionBuy, "100", "99", "98", false},
		{"stoplimit sell hits when bid < stop", domain.OrderStopLimit, domain.DirectionSell, "100", "100", "99", true},
		{"stoplimit sell misses when bid == stop", domain.OrderStopLimit, domain.DirectionSell, "100", "101", "100", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &domain.Order{OrderType: tc.typ, Direction: tc.direction, StopPrice: tc.stop}
			l := &domain.Listing{Ask: tc.ask, Bid: tc.bid}
			if got := s.stopTriggered(o, l); got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestLimitConditionMet(t *testing.T) {
	s := &Service{}
	cases := []struct {
		name      string
		direction domain.Direction
		limit     string
		ask, bid  string
		want      bool
	}{
		{"buy limit fills when ask <= limit", domain.DirectionBuy, "100", "99", "98", true},
		{"buy limit fills at limit", domain.DirectionBuy, "100", "100", "99", true},
		{"buy limit misses when ask > limit", domain.DirectionBuy, "100", "101", "100", false},
		{"sell limit fills when bid >= limit", domain.DirectionSell, "100", "101", "100", true},
		{"sell limit misses when bid < limit", domain.DirectionSell, "100", "100", "99", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &domain.Order{Direction: tc.direction, LimitPrice: tc.limit}
			l := &domain.Listing{Ask: tc.ask, Bid: tc.bid}
			if got := s.limitConditionMet(o, l); got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestLimitFillPrice(t *testing.T) {
	cases := []struct {
		name      string
		direction domain.Direction
		limit     string
		ask, bid  string
		want      string
	}{
		// Spec p.51: buy fills at min(limit, ask).
		{"buy: ask better than limit", domain.DirectionBuy, "100", "98", "97", "98"},
		{"buy: ask equals limit", domain.DirectionBuy, "100", "100", "99", "100"},
		{"buy: ask worse than limit (limit binds)", domain.DirectionBuy, "100", "101", "100", "100"},
		// Spec p.51: sell fills at max(limit, bid).
		{"sell: bid better than limit", domain.DirectionSell, "100", "102", "101", "101"},
		{"sell: bid equals limit", domain.DirectionSell, "100", "100", "100", "100"},
		{"sell: bid worse than limit (limit binds)", domain.DirectionSell, "100", "100", "99", "100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &domain.Order{Direction: tc.direction, LimitPrice: tc.limit}
			l := &domain.Listing{Ask: tc.ask, Bid: tc.bid}
			if got := limitFillPrice(o, l); got != tc.want {
				t.Fatalf("got=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestEffectiveType(t *testing.T) {
	cases := []struct {
		typ       domain.OrderType
		triggered bool
		want      domain.OrderType
	}{
		{domain.OrderMarket, false, domain.OrderMarket},
		{domain.OrderLimit, false, domain.OrderLimit},
		{domain.OrderStop, false, domain.OrderStop},
		{domain.OrderStop, true, domain.OrderMarket},
		{domain.OrderStopLimit, false, domain.OrderStopLimit},
		{domain.OrderStopLimit, true, domain.OrderLimit},
	}
	for _, tc := range cases {
		o := &domain.Order{OrderType: tc.typ, Triggered: tc.triggered}
		if got := effectiveType(o); got != tc.want {
			t.Fatalf("type=%s triggered=%v got=%s want=%s", tc.typ, tc.triggered, got, tc.want)
		}
	}
}

func TestCommissionFor(t *testing.T) {
	// commissionFor returns the WHOLE-ORDER commission, not the per-
	// fill share. The caller prorates via proratedCommission. Order
	// fields populated: Quantity, ContractSize, PricePerUnit.
	s := &Service{}
	usd := &domain.Security{Currency: domain.CurrencyUSD}
	ctx := context.Background()

	mkOrder := func(t domain.OrderType, qty int32, price string) *domain.Order {
		return &domain.Order{OrderType: t, Quantity: qty, ContractSize: "1", PricePerUnit: price}
	}

	// Market: order-total notional = 1 × 1 × 30 = 30 → 14% = 4.2,
	// well under $7 cap → 4.2.
	got, err := s.commissionFor(ctx, mkOrder(domain.OrderMarket, 1, "30"), usd, nil)
	if err != nil {
		t.Fatalf("market small: %v", err)
	}
	if got.Cmp(money.MustParse("4.2")) != 0 {
		t.Fatalf("market small got=%s want=4.2", got.String())
	}

	// Market: notional = 100 → 14% = 14, capped at $7.
	got, _ = s.commissionFor(ctx, mkOrder(domain.OrderMarket, 1, "100"), usd, nil)
	if got.Cmp(money.MustParse("7")) != 0 {
		t.Fatalf("market large got=%s want=7", got.String())
	}

	// Limit: notional = 40 → 24% = 9.6, under $12 cap.
	got, _ = s.commissionFor(ctx, mkOrder(domain.OrderLimit, 1, "40"), usd, nil)
	if got.Cmp(money.MustParse("9.6")) != 0 {
		t.Fatalf("limit small got=%s want=9.6", got.String())
	}

	// Limit: notional = 100 → 24% = 24, capped at $12.
	got, _ = s.commissionFor(ctx, mkOrder(domain.OrderLimit, 1, "100"), usd, nil)
	if got.Cmp(money.MustParse("12")) != 0 {
		t.Fatalf("limit large got=%s want=12", got.String())
	}

	// Triggered StopLimit uses the Limit table.
	o := mkOrder(domain.OrderStopLimit, 1, "30")
	o.Triggered = true
	got, _ = s.commissionFor(ctx, o, usd, nil)
	if got.Cmp(money.MustParse("7.2")) != 0 {
		t.Fatalf("stoplimit triggered got=%s want=7.2", got.String())
	}
}

// TestProratedCommission verifies the per-order cap is split across
// fills proportional to fill quantity (spec p.55-56, fix #10).
func TestProratedCommission(t *testing.T) {
	// $12 total commission on a 10-share order.
	total := money.MustParse("12")

	// Two equal 5-share fills: each pays $6.
	if got := proratedCommission(total, 5, 10); got.Cmp(money.MustParse("6")) != 0 {
		t.Fatalf("5/10 fill got=%s want=6", got.String())
	}
	// 1-of-10 share fill: $1.20.
	if got := proratedCommission(total, 1, 10); got.Cmp(money.MustParse("1.2")) != 0 {
		t.Fatalf("1/10 fill got=%s want=1.2", got.String())
	}
	// Whole-order single fill: $12.
	if got := proratedCommission(total, 10, 10); got.Cmp(money.MustParse("12")) != 0 {
		t.Fatalf("10/10 fill got=%s want=12", got.String())
	}
	// Sum of two prorated fills must equal the total commission so
	// the per-order cap is honored across the whole lifecycle.
	a := proratedCommission(total, 3, 10)
	b := proratedCommission(total, 7, 10)
	sum := money.Add(a, b)
	if sum.Cmp(total) != 0 {
		t.Fatalf("3+7 fill sum got=%s want=12", sum.String())
	}
}

// usdToSecurity should pass-through when sec.Currency is USD or RSD
// quote isn't available, and convert via the rate provider otherwise.
func TestUsdToSecurityCapConversion(t *testing.T) {
	s := &Service{Rates: stubUSDRSDRate{}}
	ctx := context.Background()

	// USD security: $7 stays as 7.
	usd := &domain.Security{Currency: domain.CurrencyUSD}
	got, err := s.usdToSecurity(ctx, usd, "7")
	if err != nil {
		t.Fatalf("usd: %v", err)
	}
	if got.Cmp(money.MustParse("7")) != 0 {
		t.Fatalf("usd cap got=%s want=7", got.String())
	}

	// RSD security: $7 × 110.50 = 773.50 RSD.
	rsd := &domain.Security{Currency: domain.CurrencyRSD}
	got, err = s.usdToSecurity(ctx, rsd, "7")
	if err != nil {
		t.Fatalf("rsd: %v", err)
	}
	if got.Cmp(money.MustParse("773.50")) != 0 {
		t.Fatalf("rsd cap got=%s want=773.50", got.String())
	}

	// No rates provider: cap stays in USD-units.
	s2 := &Service{}
	got, err = s2.usdToSecurity(ctx, rsd, "7")
	if err != nil {
		t.Fatalf("no-rates: %v", err)
	}
	if got.Cmp(money.MustParse("7")) != 0 {
		t.Fatalf("no-rates cap got=%s want=7", got.String())
	}
}

type stubUSDRSDRate struct{}

func (stubUSDRSDRate) Quote(_ context.Context, from, to domain.Currency) (string, string, error) {
	if from == domain.CurrencyUSD && to == domain.CurrencyRSD {
		return "110.20", "110.50", nil
	}
	if from == to {
		return "1", "1", nil
	}
	return "", "", nil
}

// stubSettler captures the last Settle call for assertions.
type stubSettler struct {
	called    int
	last      SettleInput
	returnOp  string
	returnErr error
}

func (s *stubSettler) Settle(_ context.Context, in SettleInput) (string, error) {
	s.called++
	s.last = in
	if s.returnErr != nil {
		return "", s.returnErr
	}
	if s.returnOp == "" {
		return in.OpID, nil
	}
	return s.returnOp, nil
}

// (The integration-shaped tests for the full executeFill pipeline live
// in execution_integration_test.go behind the build tag because they
// touch postgres.)

// Spec p.56: cap is `1440 × remaining/volume` seconds. The implementation
// scales to milliseconds so thick listings (volume ≫ remaining) still
// produce a positive sub-second cap, and thin listings (low volume) still
// produce a much larger cap — the spec's "thin → slow, thick → fast"
// pacing has to survive integer division.
func TestCadenceMaxInterval(t *testing.T) {
	cases := []struct {
		name      string
		remaining int64
		volume    int64
		want      time.Duration
	}{
		{"thick listing, single share", 1, 10000, 144 * time.Millisecond},
		{"thick listing, ten shares", 10, 10000, 1440 * time.Millisecond},
		{"thin listing", 10, 100, 144 * time.Second},
		{"very thin listing, big remaining", 100, 10, 14400 * time.Second},
		{"zero volume falls back to 1", 1, 0, 1440 * time.Second},
		{"sub-millisecond floors to 1ms", 1, 10_000_000, time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cadenceMaxInterval(c.remaining, c.volume); got != c.want {
				t.Fatalf("cadenceMaxInterval(%d,%d)=%s want=%s",
					c.remaining, c.volume, got, c.want)
			}
		})
	}

	// Differential sanity: thick is always faster than thin at equal remaining.
	thick := cadenceMaxInterval(5, 100000)
	thin := cadenceMaxInterval(5, 50)
	if thick >= thin {
		t.Fatalf("expected thick(%s) < thin(%s)", thick, thin)
	}
}

// big.Rat sanity (formatting / parsing).
func TestMoneyRoundTrip(t *testing.T) {
	r := money.MustParse("12.3456")
	if money.FormatAmount(r) != "12.3456" {
		t.Fatalf("unexpected format: %s", money.FormatAmount(r))
	}
	zero := big.NewRat(0, 1)
	if zero.Sign() != 0 {
		t.Fatalf("zero sign should be 0")
	}
}
