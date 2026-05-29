package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
)

// TestListActuaryPerformances_RequiresProfitPerm verifies the auth gate
// rejects principals without bank.profit.read (or admin).
func TestListActuaryPerformances_RequiresProfitPerm(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000099",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.ActuarySupervisor},
	})
	if _, err := svc.ListActuaryPerformances(ctx, ListActuaryPerformancesInput{}); err == nil {
		t.Fatal("expected permission denied for principal without bank.profit.read")
	}
}

// TestListBankFundPositions_RequiresProfitPerm — same gate.
func TestListBankFundPositions_RequiresProfitPerm(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000099",
		UserKind:    auth.KindClient,
		Permissions: []string{permissions.TradingClient},
	})
	if _, err := svc.ListBankFundPositions(ctx); err == nil {
		t.Fatal("expected permission denied for client principal")
	}
}

// TestGetBankProfitTimeseries_RequiresProfitPerm — same gate as the
// other Profit Banke dashboards.
func TestGetBankProfitTimeseries_RequiresProfitPerm(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000099",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.ActuaryAgent},
	})
	if _, err := svc.GetBankProfitTimeseries(ctx, GetBankProfitTimeseriesInput{}); err == nil {
		t.Fatal("expected permission denied for principal without bank.profit.read")
	}
}

// TestGetBankProfitTimeseries_RejectsBadBucket verifies an unknown
// bucket is a Validation error and never reaches date_trunc. Store is
// nil, so a regression that calls the store before validating would
// panic here.
func TestGetBankProfitTimeseries_RejectsBadBucket(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000099",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	if _, err := svc.GetBankProfitTimeseries(ctx, GetBankProfitTimeseriesInput{Bucket: "hour"}); err == nil {
		t.Fatal("expected validation error for unsupported bucket")
	}
}

// TestGetBankProfitTimeseries_RejectsInvertedWindow guards the
// from>to guard, which also runs before the store call.
func TestGetBankProfitTimeseries_RejectsInvertedWindow(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000099",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	now := time.Now()
	_, err := svc.GetBankProfitTimeseries(ctx, GetBankProfitTimeseriesInput{
		Bucket: "day",
		From:   now,
		To:     now.AddDate(0, 0, -1),
	})
	if err == nil {
		t.Fatal("expected validation error when from is after to")
	}
}

// TestReassignSupervisorAssets_RequiresAdmin verifies the
// service-to-service entry point is admin-gated (the user-svc adapter
// attaches the internal admin sentinel principal).
func TestReassignSupervisorAssets_RequiresAdmin(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000099",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.ActuarySupervisor},
	})
	if _, err := svc.ReassignSupervisorAssets(ctx,
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002"); err == nil {
		t.Fatal("expected permission denied for non-admin caller")
	}
}

// TestReassignSupervisorAssets_RejectsSelfMove guards against a quiet
// no-op when the caller accidentally targets themselves.
func TestReassignSupervisorAssets_RejectsSelfMove(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000099",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	same := "00000000-0000-0000-0000-000000000001"
	if _, err := svc.ReassignSupervisorAssets(ctx, same, same); err == nil {
		t.Fatal("expected validation error when from==to")
	}
}

// TestReassignSupervisorAssets_RejectsInvalidUUID surfaces bad input as
// Validation rather than dropping through to the SQL layer.
func TestReassignSupervisorAssets_RejectsInvalidUUID(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000099",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	if _, err := svc.ReassignSupervisorAssets(ctx, "not-a-uuid", "00000000-0000-0000-0000-000000000002"); err == nil {
		t.Fatal("expected validation error for non-UUID from_user_id")
	}
}
