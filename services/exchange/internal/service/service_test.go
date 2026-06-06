package service

import (
	"context"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/domain"
)

func authed(ctx context.Context) context.Context {
	return auth.WithPrincipal(ctx, auth.Principal{UserID: "u1", UserKind: auth.KindClient})
}

// ListRateHistory's pre-store guards are exercised without a DB: an
// unauthenticated caller, an unsupported currency, and from==to all
// return before the store is touched, so a nil Store is safe here.
func TestListRateHistory_Guards(t *testing.T) {
	svc := &Service{}

	if _, err := svc.ListRateHistory(context.Background(), domain.CurrencyEUR, domain.CurrencyRSD, 30); err == nil {
		t.Fatal("expected unauthenticated error with no principal")
	}

	ctx := authed(context.Background())

	if _, err := svc.ListRateHistory(ctx, domain.Currency("XXX"), domain.CurrencyRSD, 30); err == nil {
		t.Fatal("expected validation error for unsupported currency")
	}

	if _, err := svc.ListRateHistory(ctx, domain.CurrencyRSD, domain.CurrencyRSD, 30); err == nil {
		t.Fatal("expected validation error for from==to")
	}
}

// defaultHistoryDays is the documented "last month" window.
func TestDefaultHistoryDays(t *testing.T) {
	if defaultHistoryDays != 30 {
		t.Fatalf("defaultHistoryDays = %d, want 30", defaultHistoryDays)
	}
}
