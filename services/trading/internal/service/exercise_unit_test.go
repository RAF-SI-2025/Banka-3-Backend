package service

import (
	"context"
	"log/slog"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
)

// actuaryCtx returns a context carrying an actuary principal so the
// ExerciseOption permission gate passes and the quantity validation
// (which runs before any Store access) is reachable.
func actuaryCtx() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000099",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Actuary, permissions.ActuaryAgent},
	})
}

// TestExerciseOption_RequiresActuary verifies the spec p.61.d gate:
// only actuaries may exercise an option. A plain trading client is
// rejected before any holding lookup.
func TestExerciseOption_RequiresActuary(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-000000000098",
		UserKind:    auth.KindClient,
		Permissions: []string{permissions.TradingClient},
	})
	if _, err := svc.ExerciseOption(ctx, ExerciseOptionInput{
		HoldingID: "00000000-0000-0000-0000-000000000001",
		Quantity:  1,
	}); err == nil {
		t.Fatal("expected permission denied for non-actuary")
	}
}

// TestExerciseOption_RejectsNonPositiveQuantity pins the partial-exercise
// validation: the requested quantity must be ≥ 1. A zero or negative
// quantity is rejected before any Store access, so a nil-Store Service
// is sufficient to drive the guard.
func TestExerciseOption_RejectsNonPositiveQuantity(t *testing.T) {
	svc := &Service{Log: slog.Default()}
	ctx := actuaryCtx()

	for _, qty := range []int32{0, -1, -100} {
		if _, err := svc.ExerciseOption(ctx, ExerciseOptionInput{
			HoldingID: "00000000-0000-0000-0000-000000000001",
			Quantity:  qty,
		}); err == nil {
			t.Fatalf("expected validation error for quantity=%d", qty)
		}
	}
}
