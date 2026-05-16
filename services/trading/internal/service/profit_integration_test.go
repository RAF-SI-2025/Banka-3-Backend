//go:build integration

// Integration coverage for the Profit Banke "Profit aktuara" board
// (spec p.76). The unit suite only exercises the auth gate; this
// pins the data-path invariant the spec hinges on: the leaderboard
// is the *full* spisak svih aktuara — an actuary who never closed a
// position, or only closed at a loss, still appears with profit 0 so
// the agent-vs-supervisor comparison isn't skewed.

package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// bankProfitCtx is a principal carrying only bank.profit.read — the
// permission the Profit Banke portal actually gates on (distinct from
// the actuary.* bundle).
func bankProfitCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.BankProfitRead},
	})
}

// writeEmployeeRealizedGain seeds one realized_gains row owned by an
// employee acting as a bank actuary (user_kind='employee'), with a
// caller-chosen gain_rsd so both the profitable and loss-only paths
// can be exercised.
func writeEmployeeRealizedGain(t *testing.T, userID, secID, accID, gainRSD string) {
	t.Helper()
	const q = `
        insert into "trading".realized_gains
            (user_id, user_kind, security_id, account_id, quantity,
             cost_basis_amt, proceeds_amt, currency,
             gain_native, gain_rsd, realized_at)
        values ($1,'employee',$2,$3,1,
                100::numeric, 100::numeric, 'RSD',
                $4::numeric, $4::numeric, now())`
	if _, err := fixPool.Exec(context.Background(), q, userID, secID, accID, gainRSD); err != nil {
		t.Fatalf("seed employee realized_gain: %v", err)
	}
}

// TestIntegration_ListActuaryPerformances_IncludesZeroAndLossActuaries
// proves spec p.76's "spisak svih aktuara": every actuary_info row is
// returned, including one with no realized_gains and one whose only
// realized gain is a loss — both at profit_rsd 0 — alongside the
// profitable one, ordered by profit desc.
func TestIntegration_ListActuaryPerformances_IncludesZeroAndLossActuaries(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)
	accID := uuid.NewString()

	profitable := uuid.NewString() // agent, +1500 RSD
	loser := uuid.NewString()      // agent, one row, -800 RSD (clamped to 0)
	idle := uuid.NewString()       // supervisor, never traded

	seedActuary(t, svc, profitable, domain.ActuaryAgent, "200000", false)
	seedActuary(t, svc, loser, domain.ActuaryAgent, "200000", false)
	seedActuary(t, svc, idle, domain.ActuarySupervisor, "0", false)

	writeEmployeeRealizedGain(t, profitable, sec.ID, accID, "1500")
	writeEmployeeRealizedGain(t, loser, sec.ID, accID, "-800")

	svc.Users = &stubUsers{names: map[string]string{
		profitable: "Ana Anić",
		loser:      "Bora Borić",
		idle:       "Cveta Cvetić",
	}}

	rows, err := svc.ListActuaryPerformances(bankProfitCtx(uuid.NewString()),
		ListActuaryPerformancesInput{})
	if err != nil {
		t.Fatalf("ListActuaryPerformances: %v", err)
	}

	byID := make(map[string]*ActuaryPerformanceRow, len(rows))
	for _, r := range rows {
		byID[r.UserID] = r
	}

	// All three actuaries present — not just the profitable one.
	for _, id := range []string{profitable, loser, idle} {
		if byID[id] == nil {
			t.Fatalf("actuary %s missing from leaderboard (spec p.76 wants all)", id)
		}
	}
	if len(rows) != 3 {
		t.Fatalf("want exactly 3 actuary rows, got %d", len(rows))
	}

	if !numericEq(byID[profitable].ProfitRSD, "1500") {
		t.Errorf("profitable profit: want 1500, got %q", byID[profitable].ProfitRSD)
	}
	if byID[profitable].RealizedCount != 1 {
		t.Errorf("profitable realized_count: want 1, got %d", byID[profitable].RealizedCount)
	}

	// Loss-only actuary: clamped to 0 profit but the row still counts.
	if !numericEq(byID[loser].ProfitRSD, "0") {
		t.Errorf("loser profit: want 0 (loss clamped), got %q", byID[loser].ProfitRSD)
	}
	if byID[loser].RealizedCount != 1 {
		t.Errorf("loser realized_count: want 1, got %d", byID[loser].RealizedCount)
	}

	// Never-traded actuary: zero-padded, zero rows.
	if !numericEq(byID[idle].ProfitRSD, "0") {
		t.Errorf("idle profit: want 0, got %q", byID[idle].ProfitRSD)
	}
	if byID[idle].RealizedCount != 0 {
		t.Errorf("idle realized_count: want 0, got %d", byID[idle].RealizedCount)
	}
	if byID[idle].Type != domain.ActuarySupervisor {
		t.Errorf("idle type: want supervisor, got %q", byID[idle].Type)
	}

	// Profit-desc ordering: the only profitable actuary sorts first.
	if rows[0].UserID != profitable {
		t.Errorf("want profitable actuary first (profit desc), got %s", rows[0].UserID)
	}

	// type filter still works and also returns zero-profit matches:
	// the lone supervisor (idle, profit 0) must come back.
	sup, err := svc.ListActuaryPerformances(bankProfitCtx(uuid.NewString()),
		ListActuaryPerformancesInput{Type: domain.ActuarySupervisor})
	if err != nil {
		t.Fatalf("ListActuaryPerformances(supervisor): %v", err)
	}
	if len(sup) != 1 || sup[0].UserID != idle {
		t.Fatalf("supervisor filter: want [idle], got %+v", sup)
	}
}
