package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/store"
)

func kindOf(err error) apperr.Kind {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Kind
	}
	return apperr.KindInternal
}

// clientCtx returns a context carrying a client principal with
// payment.write, enough for SchedulePayment's permission/principal
// guards to pass so the date-validation branch is reached.
func clientCtx() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "11111111-1111-1111-1111-111111111111",
		UserKind:    auth.KindClient,
		Permissions: []string{permissions.PaymentWrite},
	})
}

func newSvcAt(now time.Time) *Service {
	return &Service{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return now },
	}
}

// TestSchedulePayment_RejectsNonFutureDate proves the spec gate
// ("Sistem proverava da li je datum u budućnosti") fires before any
// store access — a past or now date is rejected with a Validation error.
func TestSchedulePayment_RejectsNonFutureDate(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := newSvcAt(now)

	cases := []struct {
		name string
		date time.Time
	}{
		{"past", now.AddDate(0, 0, -1)},
		{"exactly now", now},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.SchedulePayment(clientCtx(), SchedulePaymentInput{
				FromAccountID:   "22222222-2222-2222-2222-222222222222",
				ToAccountNumber: "111000000000000018",
				Amount:          "100",
				RecipientName:   "Test",
				ScheduledDate:   tc.date,
			})
			if err == nil {
				t.Fatalf("expected rejection for %s date", tc.name)
			}
			if kindOf(err) != apperr.KindValidation {
				t.Fatalf("want Validation error, got %v", err)
			}
		})
	}
}

// TestSchedulePayment_RequiresPaymentWrite proves a principal without
// payment.write can't schedule.
func TestSchedulePayment_RequiresPaymentWrite(t *testing.T) {
	svc := newSvcAt(time.Now())
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:   "u",
		UserKind: auth.KindClient,
	})
	_, err := svc.SchedulePayment(ctx, SchedulePaymentInput{
		ScheduledDate: time.Now().Add(48 * time.Hour),
	})
	if err == nil || kindOf(err) != apperr.KindPermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}
}

// TestIsInsufficientFunds routes the store sentinel to the 'failed'
// branch of the due-sweep while leaving unrelated errors to retry.
func TestIsInsufficientFunds(t *testing.T) {
	if !isInsufficientFunds(store.ErrInsufficientFunds) {
		t.Fatal("ErrInsufficientFunds must be detected")
	}
	if isInsufficientFunds(apperr.Internal("boom", nil)) {
		t.Fatal("unrelated error must not be treated as insufficient funds")
	}
	if isInsufficientFunds(nil) {
		t.Fatal("nil must not be treated as insufficient funds")
	}
}
