package service

import (
	"testing"
	"time"

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

// TestDaysUntilExpiry validates the calendar-day distance predicate used
// by the S63 pre-expiry warning sweep. Crucially, the function must
// respect calendar day boundaries in the Europe/Belgrade timezone (not UTC),
// and it must return exactly 3 on the single day that triggers the warning.
func TestDaysUntilExpiry(t *testing.T) {
	belgrade, err := time.LoadLocation("Europe/Belgrade")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	// Anchor: "today" is 2026-06-06 09:00 Belgrade = 07:00 UTC.
	now := time.Date(2026, 6, 6, 9, 0, 0, 0, belgrade)

	cases := []struct {
		name       string
		settlement time.Time // in UTC for realism (DB stores UTC)
		wantDays   int
	}{
		{
			name:       "expires today (midnight Belgrade)",
			settlement: time.Date(2026, 6, 6, 0, 0, 0, 0, belgrade).UTC(),
			wantDays:   0,
		},
		{
			name:       "expires tomorrow",
			settlement: time.Date(2026, 6, 7, 0, 0, 0, 0, belgrade).UTC(),
			wantDays:   1,
		},
		{
			name:       "expires in exactly 3 days — warning fires",
			settlement: time.Date(2026, 6, 9, 0, 0, 0, 0, belgrade).UTC(),
			wantDays:   3,
		},
		{
			name:       "expires in 4 days — no warning",
			settlement: time.Date(2026, 6, 10, 0, 0, 0, 0, belgrade).UTC(),
			wantDays:   4,
		},
		{
			name: "settlement late-night UTC crosses midnight Belgrade: 2026-06-08T23:30 UTC = 2026-06-09T01:30 Belgrade",
			// settlement_date stored as 2026-06-09 midnight Belgrade = 2026-06-08 22:00 UTC
			// "now" is 2026-06-06 09:00 Belgrade → daysUntil should be 3
			settlement: time.Date(2026, 6, 8, 22, 0, 0, 0, time.UTC),
			wantDays:   3,
		},
		{
			name: "warning day: same calendar date from end-of-day 'now'",
			// now near end of business on the warning day
			settlement: time.Date(2026, 6, 9, 0, 0, 0, 0, belgrade).UTC(),
			wantDays:   3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := daysUntilExpiry(now, tc.settlement, belgrade)
			if got != tc.wantDays {
				t.Fatalf("daysUntilExpiry: want %d got %d (settlement=%v now=%v)",
					tc.wantDays, got, tc.settlement, now)
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
