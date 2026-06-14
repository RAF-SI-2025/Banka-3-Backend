package router

import (
	"testing"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
)

// Banka-2 infers the OTC seller role (spec p.79 — clients trade with
// clients, actuaries with actuaries) from a C-/E- prefix on the seller id
// we serve over /public-stock, and echoes that prefixed id back on the
// negotiation/settlement/user round-trip. These tests pin the prefix
// contract + the strip round-trip + the presented-routing recognition.

func TestBanka2PrefixSellerID(t *testing.T) {
	cases := []struct {
		name string
		kind tradingpb.UserKind
		id   string
		want string
	}{
		{"client", tradingpb.UserKind_USER_KIND_CLIENT, "6f661c77-uuid", "C-6f661c77-uuid"},
		{"employee", tradingpb.UserKind_USER_KIND_EMPLOYEE, "abc", "E-abc"},
		{"unspecified defaults to client", tradingpb.UserKind_USER_KIND_UNSPECIFIED, "x", "C-x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := banka2PrefixSellerID(c.kind, c.id); got != c.want {
				t.Errorf("banka2PrefixSellerID(%v, %q) = %q, want %q", c.kind, c.id, got, c.want)
			}
		})
	}
}

func TestBanka2StripSellerID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"C-6f661c77-uuid", "6f661c77-uuid"},
		{"E-abc", "abc"},
		{"6f661c77-uuid", "6f661c77-uuid"}, // bare id: unchanged (native / non-Banka-2 callers)
		{"", ""},
	}
	for _, c := range cases {
		if got := banka2StripSellerID(c.in); got != c.want {
			t.Errorf("banka2StripSellerID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBanka2SellerIDRoundTrip(t *testing.T) {
	for _, kind := range []tradingpb.UserKind{
		tradingpb.UserKind_USER_KIND_CLIENT,
		tradingpb.UserKind_USER_KIND_EMPLOYEE,
	} {
		id := "6f661c77-67ab-4178-866c-9c7c1f7cdb94"
		if got := banka2StripSellerID(banka2PrefixSellerID(kind, id)); got != id {
			t.Errorf("round-trip for %v: got %q, want %q", kind, got, id)
		}
	}
}

func TestIsOurRouting(t *testing.T) {
	// Banka-2 (partner 222) knows us as 265; our real routing is 333.
	p := &PartnerBanka2{
		BankRoutingNumber: "333",
		PresentedRouting:  map[string]string{"222": "265"},
	}
	cases := []struct {
		routing string
		want    bool
	}{
		{"333", true}, // our real routing
		{"265", true}, // the routing we present to Banka-2
		{"222", false},
		{"999", false},
	}
	for _, c := range cases {
		if got := p.isOurRouting(c.routing); got != c.want {
			t.Errorf("isOurRouting(%q) = %v, want %v", c.routing, got, c.want)
		}
	}

	// Empty BankRoutingNumber defaults to 333.
	def := &PartnerBanka2{}
	if !def.isOurRouting("333") {
		t.Error("isOurRouting(333) with empty BankRoutingNumber should default-accept 333")
	}
}
