package service

import (
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

func TestAssertSameKindCounterparties(t *testing.T) {
	cases := []struct {
		name       string
		buyerKind  auth.UserKind
		sellerKind domain.UserKind
		wantErr    bool
	}{
		{"client-client ok", auth.KindClient, domain.KindClient, false},
		{"employee-employee ok", auth.KindEmployee, domain.KindEmployee, false},
		{"client-employee mixed rejected (EDGE-8)", auth.KindClient, domain.KindEmployee, true},
		{"employee-client mixed rejected (EDGE-8)", auth.KindEmployee, domain.KindClient, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := assertSameKindCounterparties(
				auth.Principal{UserKind: c.buyerKind},
				&domain.Holding{UserKind: c.sellerKind},
			)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v gotErr=%v", c.wantErr, err)
			}
		})
	}
}

func TestRequireOTCTrader(t *testing.T) {
	cases := []struct {
		name    string
		perms   []string
		wantErr bool
	}{
		{"client trading", []string{permissions.TradingClient}, false},
		{"supervisor trade", []string{permissions.OTCTradeSupervisor}, false},
		{"admin", []string{permissions.Admin}, false},
		{"unrelated perm", []string{permissions.ClientRead}, true},
		{"none", []string{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := requireOTCTrader(auth.Principal{Permissions: c.perms})
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v gotErr=%v", c.wantErr, err)
			}
		})
	}
}

func TestValidateOTCMoneyFields(t *testing.T) {
	cases := []struct {
		name    string
		qty     int32
		price   string
		premium string
		wantErr bool
	}{
		{"happy", 10, "150.00", "5.00", false},
		{"zero qty", 0, "150", "5", true},
		{"negative price", 10, "-1", "5", true},
		// Empty premium parses to 0 (permitted by validator; wire-level
		// buf.validate.string.min_len catches the empty case before
		// reaching this code).
		{"empty premium permitted", 10, "150", "", false},
		{"non-numeric", 10, "abc", "5", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateOTCMoneyFields(c.qty, c.price, c.premium)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v gotErr=%v", c.wantErr, err)
			}
		})
	}
}

func TestOtherParty(t *testing.T) {
	o := &domain.OTCOffer{
		BuyerID:    "buyer",
		BuyerKind:  domain.KindClient,
		SellerID:   "seller",
		SellerKind: domain.KindClient,
	}
	id, kind := otherParty(o, "buyer")
	if id != "seller" || kind != domain.KindClient {
		t.Fatalf("from buyer: %s/%s", id, kind)
	}
	id, kind = otherParty(o, "seller")
	if id != "buyer" || kind != domain.KindClient {
		t.Fatalf("from seller: %s/%s", id, kind)
	}
}
