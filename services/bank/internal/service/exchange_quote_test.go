package service

import (
	"context"
	"strings"
	"testing"

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

// TestQuote_ForeignToRSD: client sells 10 EUR. Bank uses BID 117.20.
//   raw = 10 × 117.20 = 1172.0000 RSD
//   commission = 1172.0 × 0.005 = 5.8600 RSD
//   net = 1166.1400 RSD
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
	if q.ToAmount != "1166.1400" {
		t.Errorf("net to-amount: got %s, want 1166.1400", q.ToAmount)
	}
	if q.Commission != "5.8600" {
		t.Errorf("commission: got %s, want 5.8600", q.Commission)
	}
	// Composite rate = 117.20.
	if q.Rate[:6] != "117.20" {
		t.Errorf("rate: got %s, want 117.20…", q.Rate)
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
	if q.ToAmount != "1172.0000" {
		t.Errorf("raw to-amount: got %s, want 1172.0000", q.ToAmount)
	}
	if q.Commission != "0.0000" {
		t.Errorf("commission should be zero, got %s", q.Commission)
	}
}

// TestQuote_CrossCurrency walks the menjačnica formula end-to-end:
// EUR → USD never has a direct rate; bank converts EUR→RSD at BID, then
// RSD→USD at ASK.
//
//   100 EUR × 117.20 (EUR bid) = 11720 RSD
//   11720 RSD / 110.50 (USD ask) = 106.0633 USD
//   commission = 106.0633 × 0.005 = 0.5303 USD
//   net = 105.5330 USD
//   composite rate = 117.20 / 110.50 = 1.06063…
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
	// ToAmount: derived above. Allow a few decimal-rounding wiggle.
	if !strings.HasPrefix(q.ToAmount, "105.5") {
		t.Errorf("to-amount: got %s, want ~105.53", q.ToAmount)
	}
	if !strings.HasPrefix(q.Rate, "1.0606") {
		t.Errorf("rate: got %s, want ~1.0606", q.Rate)
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
