package service

import (
	"context"
	"log/slog"
	"math/big"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

func TestValidateOrderShape(t *testing.T) {
	cases := []struct {
		name   string
		in     CreateOrderInput
		wantOK bool
	}{
		{
			name: "market buy ok",
			in: CreateOrderInput{
				SecurityID: "sid", AccountID: "aid",
				OrderType: domain.OrderMarket, Direction: domain.DirectionBuy,
				Quantity: 1,
			},
			wantOK: true,
		},
		{
			name: "limit without limit_price",
			in: CreateOrderInput{
				SecurityID: "sid", AccountID: "aid",
				OrderType: domain.OrderLimit, Direction: domain.DirectionBuy,
				Quantity: 1,
			},
			wantOK: false,
		},
		{
			name: "limit with limit_price",
			in: CreateOrderInput{
				SecurityID: "sid", AccountID: "aid",
				OrderType: domain.OrderLimit, Direction: domain.DirectionBuy,
				Quantity: 1, LimitPrice: "100.00",
			},
			wantOK: true,
		},
		{
			name: "stop without stop_price",
			in: CreateOrderInput{
				SecurityID: "sid", AccountID: "aid",
				OrderType: domain.OrderStop, Direction: domain.DirectionSell,
				Quantity: 1,
			},
			wantOK: false,
		},
		{
			name: "stop_limit needs both",
			in: CreateOrderInput{
				SecurityID: "sid", AccountID: "aid",
				OrderType: domain.OrderStopLimit, Direction: domain.DirectionBuy,
				Quantity: 1, LimitPrice: "100", StopPrice: "95",
			},
			wantOK: true,
		},
		{
			name: "stop_limit missing limit",
			in: CreateOrderInput{
				SecurityID: "sid", AccountID: "aid",
				OrderType: domain.OrderStopLimit, Direction: domain.DirectionBuy,
				Quantity: 1, StopPrice: "95",
			},
			wantOK: false,
		},
		{
			name: "qty zero rejected",
			in: CreateOrderInput{
				SecurityID: "sid", AccountID: "aid",
				OrderType: domain.OrderMarket, Direction: domain.DirectionBuy,
				Quantity: 0,
			},
			wantOK: false,
		},
		{
			name:   "missing security_id",
			in:     CreateOrderInput{AccountID: "aid", OrderType: domain.OrderMarket, Direction: domain.DirectionBuy, Quantity: 1},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOrderShape(tc.in)
			got := err == nil
			if got != tc.wantOK {
				t.Fatalf("got ok=%v err=%v, want ok=%v", got, err, tc.wantOK)
			}
		})
	}
}

func TestAssertTraderRole(t *testing.T) {
	s := &Service{}
	cases := []struct {
		name  string
		perms []string
		want  bool
	}{
		{"client trading", []string{permissions.TradingClient}, true},
		{"agent", []string{permissions.Actuary, permissions.ActuaryAgent}, true},
		{"supervisor", []string{permissions.Actuary, permissions.ActuarySupervisor}, true},
		{"admin", []string{permissions.Admin}, true},
		{"plain employee", []string{permissions.EmployeeRead}, false},
		{"plain client", []string{permissions.ClientRead}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.assertTraderRole(auth.Principal{Permissions: tc.perms})
			got := err == nil
			if got != tc.want {
				t.Fatalf("got=%v err=%v, want=%v", got, err, tc.want)
			}
		})
	}
}

// stubRates lets us drive the FX-conversion path without an exchange
// service. Returns a fixed bid/ask regardless of currency pair.
type stubRates struct {
	ask string
}

func (s *stubRates) Quote(_ context.Context, _, _ domain.Currency) (string, string, error) {
	return s.ask, s.ask, nil
}

func TestTradeValueRSD(t *testing.T) {
	svc := &Service{Log: slog.Default(), Rates: &stubRates{ask: "120"}}

	// USD security, qty=10, contract_size=1, price=50  → notional=500 USD
	// At 120 RSD/USD that's 60_000 RSD.
	rsd, err := svc.tradeValueRSD(context.Background(),
		&domain.Security{ID: "x", Currency: domain.CurrencyUSD},
		10, "50", "1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := new(big.Rat).SetInt64(60000)
	if rsd.Cmp(want) != 0 {
		t.Fatalf("rsd=%s want=%s", rsd.String(), want.String())
	}

	// RSD security skips the rate provider entirely.
	rsd, err = svc.tradeValueRSD(context.Background(),
		&domain.Security{ID: "y", Currency: domain.CurrencyRSD},
		10, "50", "1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rsd.Cmp(new(big.Rat).SetInt64(500)) != 0 {
		t.Fatalf("rsd-trade got=%s want=500", rsd.String())
	}

	// No rates provider falls back to raw notional with a warning logged.
	svc2 := &Service{Log: slog.Default()}
	rsd, err = svc2.tradeValueRSD(context.Background(),
		&domain.Security{ID: "z", Currency: domain.CurrencyEUR},
		2, "100", "10")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rsd.Cmp(new(big.Rat).SetInt64(2000)) != 0 {
		t.Fatalf("fallback got=%s want=2000", rsd.String())
	}
}
