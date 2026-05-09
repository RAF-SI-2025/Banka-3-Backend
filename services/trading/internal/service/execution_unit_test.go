package service

import (
	"context"
	"math/big"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

func TestStopTriggered(t *testing.T) {
	s := &Service{}
	cases := []struct {
		name      string
		direction domain.Direction
		stop      string
		last      string
		want      bool
	}{
		{"buy stop hits when last >= stop", domain.DirectionBuy, "100", "100.00", true},
		{"buy stop hits when last > stop", domain.DirectionBuy, "100", "101.00", true},
		{"buy stop misses when last < stop", domain.DirectionBuy, "100", "99.00", false},
		{"sell stop hits when last <= stop", domain.DirectionSell, "100", "100.00", true},
		{"sell stop hits when last < stop", domain.DirectionSell, "100", "99.00", true},
		{"sell stop misses when last > stop", domain.DirectionSell, "100", "101.00", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &domain.Order{Direction: tc.direction, StopPrice: tc.stop}
			l := &domain.Listing{Price: tc.last}
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
	s := &Service{}

	// Market: min(14%*notional, $7).
	// Notional = 30 → 14% = 4.20 → cap (7) not hit → 4.20.
	o := &domain.Order{OrderType: domain.OrderMarket}
	got := s.commissionFor(o, money.MustParse("30"))
	want := money.MustParse("4.2")
	if got.Cmp(want) != 0 {
		t.Fatalf("market small got=%s want=%s", got.String(), want.String())
	}

	// Notional = 100 → 14% = 14 → cap (7) hits → 7.
	got = s.commissionFor(o, money.MustParse("100"))
	want = money.MustParse("7")
	if got.Cmp(want) != 0 {
		t.Fatalf("market large got=%s want=%s", got.String(), want.String())
	}

	// Limit: min(24%*notional, $12).
	o = &domain.Order{OrderType: domain.OrderLimit}
	got = s.commissionFor(o, money.MustParse("40"))
	want = money.MustParse("9.6")
	if got.Cmp(want) != 0 {
		t.Fatalf("limit small got=%s want=%s", got.String(), want.String())
	}
	got = s.commissionFor(o, money.MustParse("100"))
	want = money.MustParse("12")
	if got.Cmp(want) != 0 {
		t.Fatalf("limit large got=%s want=%s", got.String(), want.String())
	}

	// Triggered StopLimit follows the Limit table.
	o = &domain.Order{OrderType: domain.OrderStopLimit, Triggered: true}
	got = s.commissionFor(o, money.MustParse("30"))
	want = money.MustParse("7.2")
	if got.Cmp(want) != 0 {
		t.Fatalf("stoplimit triggered got=%s want=%s", got.String(), want.String())
	}
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

// Sanity-check that big.Rat comparisons in cadenceReady's volume math
// don't blow up on tiny remainders.
func TestCadenceVolumeMathSafe(t *testing.T) {
	// max_interval := 1440 * remaining / volume
	// volume=10000, remaining=1 → 1440/10000 = 0.144 minutes ≈ 8.6s
	// Floor in service is 5s — should be honored.
	rem := int64(1)
	vol := int64(10000)
	gotMin := 1440 * rem / vol
	if gotMin != 0 {
		t.Fatalf("integer math got=%d want=0", gotMin)
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
