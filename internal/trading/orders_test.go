package trading

import (
	"testing"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestParseOrderType(t *testing.T) {
	cases := []struct {
		in   string
		want OrderType
		ok   bool
	}{
		{"market", OrderMarket, true},
		{"limit", OrderLimit, true},
		{"stop", OrderStop, true},
		{"stop_limit", OrderStopLimit, true},
		{"MARKET", "", false},
		{"", "", false},
		{"bogus", "", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseOrderType(c.in)
			if c.ok {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != c.want {
					t.Errorf("got %q, want %q", got, c.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for %q", c.in)
			}
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("code = %s, want InvalidArgument", status.Code(err))
			}
		})
	}
}

func TestParseDirection(t *testing.T) {
	cases := []struct {
		in   string
		want OrderDirection
		ok   bool
	}{
		{"buy", DirectionBuy, true},
		{"sell", DirectionSell, true},
		{"BUY", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseDirection(c.in)
			if c.ok {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != c.want {
					t.Errorf("got %q, want %q", got, c.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for %q", c.in)
			}
		})
	}
}

func TestValidatePriceFields(t *testing.T) {
	cases := []struct {
		name    string
		ot      OrderType
		limit   int64
		stop    int64
		wantErr bool
	}{
		{"market/empty", OrderMarket, 0, 0, false},
		{"market/limit-set", OrderMarket, 100, 0, true},
		{"market/stop-set", OrderMarket, 0, 100, true},

		{"limit/limit-set", OrderLimit, 100, 0, false},
		{"limit/missing", OrderLimit, 0, 0, true},
		{"limit/negative", OrderLimit, -1, 0, true},
		{"limit/stop-also-set", OrderLimit, 100, 50, true},

		{"stop/stop-set", OrderStop, 0, 100, false},
		{"stop/missing", OrderStop, 0, 0, true},
		{"stop/limit-also-set", OrderStop, 50, 100, true},

		{"stop_limit/both-set", OrderStopLimit, 100, 50, false},
		{"stop_limit/only-limit", OrderStopLimit, 100, 0, true},
		{"stop_limit/only-stop", OrderStopLimit, 0, 100, true},
		{"stop_limit/neither", OrderStopLimit, 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePriceFields(c.ot, c.limit, c.stop)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				if status.Code(err) != codes.InvalidArgument {
					t.Errorf("code = %s, want InvalidArgument", status.Code(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestMarketReferencePrice(t *testing.T) {
	cases := []struct {
		name string
		ot   OrderType
		req  *tradingpb.CreateOrderRequest
		want int64
	}{
		{"market", OrderMarket, &tradingpb.CreateOrderRequest{}, 0},
		{"limit", OrderLimit, &tradingpb.CreateOrderRequest{LimitPrice: 150}, 150},
		{"stop", OrderStop, &tradingpb.CreateOrderRequest{StopPrice: 120}, 120},
		{"stop_limit", OrderStopLimit, &tradingpb.CreateOrderRequest{LimitPrice: 150, StopPrice: 120}, 150},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := marketReferencePrice(c.ot, c.req); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
