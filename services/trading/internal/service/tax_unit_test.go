package service

import (
	"context"
	"log/slog"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

func TestTaxRateConstant(t *testing.T) {
	// Belt-and-suspenders: the spec p.62 tax rate is 15%. If anyone
	// nudges the literal in tax.go this test catches it.
	if got := taxRate.FloatString(2); got != "0.15" {
		t.Fatalf("taxRate=%s want 0.15", got)
	}
}

func TestTaxCronContextStampsAdmin(t *testing.T) {
	ctx := TaxCronContext(context.Background())
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		t.Fatal("expected principal on cron context")
	}
	if !permissions.Has(p.Permissions, permissions.Admin) {
		t.Fatalf("expected admin permission, got %v", p.Permissions)
	}
	if p.UserKind != auth.KindEmployee {
		t.Fatalf("expected employee kind, got %v", p.UserKind)
	}
}

// stubTaxSettler records calls so RunTax behaviour can be asserted in
// integration tests; included here for completeness of the unit-level
// surface even though RunTax itself is exercised through integration.
type stubTaxSettler struct {
	calls []TaxSettleInput
}

func (s *stubTaxSettler) SettleTax(_ context.Context, in TaxSettleInput) (string, error) {
	s.calls = append(s.calls, in)
	return in.OpID, nil
}

// TestRunTax_RequiresSupervisor verifies that the auth gate rejects
// principals lacking admin/supervisor.
func TestRunTax_RequiresSupervisor(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000099",
		UserKind:    auth.KindClient,
		Permissions: []string{permissions.TradingClient},
	})
	if _, err := svc.RunTax(ctx, RunTaxInput{}); err == nil {
		t.Fatal("expected permission denied for non-supervisor")
	}
}

// TestListTaxPositions_RequiresSupervisor — same gate.
func TestListTaxPositions_RequiresSupervisor(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000099",
		UserKind:    auth.KindClient,
		Permissions: []string{permissions.TradingClient},
	})
	if _, err := svc.ListTaxPositions(ctx, ListTaxPositionsInput{UserKind: domain.KindClient}); err == nil {
		t.Fatal("expected permission denied for non-supervisor")
	}
}
