package service

import (
	"context"
	"strings"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// stubRates is an in-memory RateProvider keyed by (from,to).
type stubRates struct {
	bid map[string]string
	ask map[string]string
}

func (s *stubRates) Quote(_ context.Context, from, to domain.Currency) (string, string, error) {
	k := string(from) + "/" + string(to)
	return s.bid[k], s.ask[k], nil
}

func newSvc(t *testing.T, rates *stubRates) *Service {
	t.Helper()
	svc := &Service{Cfg: Config{FXCommission: "0.005"}}
	svc.Rates = rates
	return svc
}

// TestQuote_SameCurrency_NoConversion documents the spec p.26 short-
// circuit: if from == to, ToAmount == FromAmount and commission is 0.
// We don't even need a rate provider in this path.
func TestQuote_SameCurrency_NoConversion(t *testing.T) {
	svc := newSvc(t, nil)
	q, err := svc.QuoteExchange(context.Background(), domain.CurrencyEUR, domain.CurrencyEUR, "100", true)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.FromAmount != "100.0000" || q.ToAmount != "100.0000" {
		t.Errorf("amounts: got %s → %s", q.FromAmount, q.ToAmount)
	}
	if q.Commission != "0.0000" {
		t.Errorf("commission: got %s, want 0", q.Commission)
	}
	if q.Rate != "" {
		t.Errorf("rate should be empty for same-currency, got %s", q.Rate)
	}
}

// TestQuote_RSDToForeign mirrors the spec p.26 worked example:
// "buy 10 EUR" — bank uses ASK of (EUR,RSD); commission 0.5%
// is taken on the destination side. With ASK 117.50:
//   raw = 1175 / 117.50 = 10.0000 EUR
//   commission = 10.0000 × 0.005 = 0.0500 EUR
//   net = 9.9500 EUR
func TestQuote_RSDToForeign(t *testing.T) {
	rates := &stubRates{
		bid: map[string]string{"EUR/RSD": "117.20"},
		ask: map[string]string{"EUR/RSD": "117.50"},
	}
	svc := newSvc(t, rates)
	q, err := svc.QuoteExchange(context.Background(), domain.CurrencyRSD, domain.CurrencyEUR, "1175", true)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.ToAmount != "9.9500" {
		t.Errorf("net to-amount: got %s, want 9.9500", q.ToAmount)
	}
	if q.Commission != "0.0500" {
		t.Errorf("commission: got %s, want 0.0500", q.Commission)
	}
	// 1 RSD = 1/117.50 EUR ≈ 0.00851063
	if !strings.HasPrefix(q.Rate, "0.0085") {
		t.Errorf("rate: got %s, want 0.0085…", q.Rate)
	}
}

// TestQuote_ForeignToRSD: client sells 10 EUR. Per spec p.26 the bank
// uses the ASK column on every leg (117.50), even when buying foreign.
// Profit comes from the commission, not the spread.
//   raw = 10 × 117.50 = 1175.0000 RSD
//   commission = 1175.0 × 0.005 = 5.8750 RSD
//   net = 1169.1250 RSD
func TestQuote_ForeignToRSD(t *testing.T) {
	rates := &stubRates{
		bid: map[string]string{"EUR/RSD": "117.20"},
		ask: map[string]string{"EUR/RSD": "117.50"},
	}
	svc := newSvc(t, rates)
	q, err := svc.QuoteExchange(context.Background(), domain.CurrencyEUR, domain.CurrencyRSD, "10", true)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.ToAmount != "1169.1250" {
		t.Errorf("net to-amount: got %s, want 1169.1250", q.ToAmount)
	}
	if q.Commission != "5.8750" {
		t.Errorf("commission: got %s, want 5.8750", q.Commission)
	}
	if q.Rate[:6] != "117.50" {
		t.Errorf("rate: got %s, want 117.50…", q.Rate)
	}
}

// TestQuote_NoCommissionPath is the "raw" preview the rates calculator
// uses (e.g. capital-gains tax conversion later in c3 also uses this
// path — no commission, only the rate).
func TestQuote_NoCommission(t *testing.T) {
	rates := &stubRates{
		bid: map[string]string{"EUR/RSD": "117.20"},
		ask: map[string]string{"EUR/RSD": "117.50"},
	}
	svc := newSvc(t, rates)
	q, err := svc.QuoteExchange(context.Background(), domain.CurrencyEUR, domain.CurrencyRSD, "10", false)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.ToAmount != "1175.0000" {
		t.Errorf("raw to-amount: got %s, want 1175.0000", q.ToAmount)
	}
	if q.Commission != "0.0000" {
		t.Errorf("commission should be zero, got %s", q.Commission)
	}
}

// TestQuote_CrossCurrency walks the menjačnica formula end-to-end:
// EUR → USD never has a direct rate; bank converts EUR→RSD at the EUR
// ASK, then RSD→USD at the USD ASK (spec p.26 primer 2).
//
//   100 EUR × 117.50 (EUR ask) = 11750 RSD
//   11750 RSD / 110.50 (USD ask) = 106.3348 USD
//   commission = 106.3348 × 0.005 = 0.5317 USD
//   net = 105.8031 USD
//   composite rate = 117.50 / 110.50 ≈ 1.06334…
func TestQuote_CrossCurrency(t *testing.T) {
	rates := &stubRates{
		bid: map[string]string{"EUR/RSD": "117.20", "USD/RSD": "110.20"},
		ask: map[string]string{"EUR/RSD": "117.50", "USD/RSD": "110.50"},
	}
	svc := newSvc(t, rates)
	q, err := svc.QuoteExchange(context.Background(), domain.CurrencyEUR, domain.CurrencyUSD, "100", true)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.FromAmount != "100.0000" {
		t.Errorf("from-amount: got %s", q.FromAmount)
	}
	if !strings.HasPrefix(q.ToAmount, "105.8") {
		t.Errorf("to-amount: got %s, want ~105.80", q.ToAmount)
	}
	if !strings.HasPrefix(q.Rate, "1.0633") {
		t.Errorf("rate: got %s, want ~1.0633", q.Rate)
	}
}

func TestQuote_RejectsZeroOrNegativeAmount(t *testing.T) {
	svc := newSvc(t, &stubRates{})
	for _, in := range []string{"0", "-10", ""} {
		if _, err := svc.QuoteExchange(context.Background(), domain.CurrencyEUR, domain.CurrencyRSD, in, true); err == nil {
			t.Errorf("amount %q: expected error", in)
		}
	}
}

func TestQuote_RejectsUnsupportedCurrency(t *testing.T) {
	svc := newSvc(t, &stubRates{})
	if _, err := svc.QuoteExchange(context.Background(), domain.Currency("ZZZ"), domain.CurrencyRSD, "10", true); err == nil {
		t.Error("expected unsupported-currency error")
	}
}

// TestQuote_NoRatesProvider proves the explicit "exchange rate provider
// not configured" surface — important because the bank service can boot
// without a rates wire and we want a human-readable failure rather than
// a nil-pointer panic.
func TestQuote_NoRatesProvider(t *testing.T) {
	svc := &Service{Cfg: Config{FXCommission: "0.005"}}
	if _, err := svc.QuoteExchange(context.Background(), domain.CurrencyEUR, domain.CurrencyRSD, "10", true); err == nil {
		t.Error("expected error when Rates is nil")
	}
}

// TestQuote_ActuarySkipsCommission pins the spec edge-case (CLAUDE.md
// #2): when the calling principal carries the Actuary permission, FX
// commission is zero. Same inputs as TestQuote_ForeignToRSD; only
// the principal differs.
func TestQuote_ActuarySkipsCommission(t *testing.T) {
	rates := &stubRates{
		bid: map[string]string{"EUR/RSD": "117.20"},
		ask: map[string]string{"EUR/RSD": "117.50"},
	}
	svc := newSvc(t, rates)

	actuaryCtx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "anyone",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Actuary},
	})

	q, err := svc.QuoteExchange(actuaryCtx, domain.CurrencyEUR, domain.CurrencyRSD, "10", true)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.Commission != "0.0000" {
		t.Errorf("actuary commission: got %s, want 0.0000", q.Commission)
	}
	// Without commission, the actuary gets the full conversion: 10 × 117.50.
	if q.ToAmount != "1175.0000" {
		t.Errorf("actuary to-amount: got %s, want 1175.0000", q.ToAmount)
	}
}

// TestCommissionRateFor_DefaultsToConfigured confirms a non-actuary
// principal still pays the configured rate. Unit test on the helper
// itself so the c3 actuary-trade path doesn't have to introduce a new
// regression.
func TestCommissionRateFor_DefaultsToConfigured(t *testing.T) {
	svc := &Service{Cfg: Config{FXCommission: "0.0080"}}
	for _, p := range []auth.Principal{
		{}, // no permissions
		{Permissions: []string{permissions.PaymentWrite}},
		{UserKind: auth.KindClient, Permissions: []string{permissions.ClientRead}},
	} {
		got := svc.commissionRateFor(p)
		if got.Cmp(svc.commissionRate()) != 0 {
			t.Errorf("non-actuary commission: got %v, want configured %v (perms=%v)", got, svc.commissionRate(), p.Permissions)
		}
	}
}
