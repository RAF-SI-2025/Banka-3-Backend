package service

import (
	"context"
	"log/slog"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
)

// stubDividendBank stubs the BankReservations surface the dividend
// account-routing needs. accountErr forces AccountAvailable to fail
// (simulating a deleted purchase account, S55). accountCur is the
// purchase account's currency. byCurrency maps a currency to the
// accounts ListClientAccounts should return.
type stubDividendBank struct {
	accountErr error
	accountCur domain.Currency
	byCurrency map[domain.Currency][]BankAccount
}

func (b *stubDividendBank) AccountAvailable(ctx context.Context, accountID string) (domain.Currency, string, error) {
	if b.accountErr != nil {
		return "", "", b.accountErr
	}
	return b.accountCur, "1000", nil
}

func (b *stubDividendBank) ListClientAccounts(ctx context.Context, ownerID string, currency domain.Currency) ([]BankAccount, error) {
	return b.byCurrency[currency], nil
}

// Unused BankReservations methods — only AccountAvailable +
// ListClientAccounts are exercised by resolveDividendAccount.
func (b *stubDividendBank) Reserve(ctx context.Context, in ReserveInput) (string, error) {
	return "", nil
}
func (b *stubDividendBank) Release(ctx context.Context, opID string) (bool, error) { return false, nil }
func (b *stubDividendBank) Commit(ctx context.Context, in CommitInput) (string, error) {
	return "", nil
}
func (b *stubDividendBank) Transfer(ctx context.Context, in TransferInput) (string, error) {
	return "", nil
}
func (b *stubDividendBank) CreateFundAccount(ctx context.Context, name string, currency domain.Currency) (string, error) {
	return "", nil
}
func (b *stubDividendBank) AccountNumber(ctx context.Context, accountID string) (string, error) {
	return "", nil
}
func (b *stubDividendBank) SettleDividend(ctx context.Context, in DividendSettleInput) (string, error) {
	return in.OpID, nil
}

var _ BankReservations = (*stubDividendBank)(nil)

func newDivService(bank BankReservations) *Service {
	return &Service{Log: slog.Default(), Reservations: bank}
}

func TestComputeDividend(t *testing.T) {
	s := newDivService(nil)
	// 50 AAPL, yield 0.5%, price $200 → 50 × 200 × 0.00125 = 12.50 (S54).
	gross, ok := s.computeDividend(&store.DividendCandidate{
		Quantity: 50, Price: "200", DividendYield: "0.005",
	})
	if !ok {
		t.Fatal("expected a positive dividend")
	}
	if got := gross.FloatString(2); got != "12.50" {
		t.Fatalf("dividend=%s want 12.50", got)
	}

	// Zero yield → no payout.
	if _, ok := s.computeDividend(&store.DividendCandidate{Quantity: 50, Price: "200", DividendYield: "0"}); ok {
		t.Fatal("zero yield should yield no dividend")
	}
}

func TestResolveDividendAccount_Bank_S58(t *testing.T) {
	// Actuary "in the name of the bank": credit the holding's own
	// (bank-owned forex_book) account directly — Profit Banke, no
	// fallback lookups.
	s := newDivService(&stubDividendBank{})
	acct, bankDest, err := s.resolveDividendAccount(context.Background(),
		&store.DividendCandidate{AccountID: "fb-usd", UserKind: domain.KindEmployee, Currency: domain.CurrencyUSD},
		false)
	if err != nil {
		t.Fatal(err)
	}
	if acct != "fb-usd" || !bankDest {
		t.Fatalf("got acct=%s bankDest=%v want fb-usd/true", acct, bankDest)
	}
}

func TestResolveDividendAccount_PurchaseAccount_S54(t *testing.T) {
	// Purchase account exists and matches the security currency → use it.
	bank := &stubDividendBank{accountCur: domain.CurrencyUSD}
	s := newDivService(bank)
	acct, bankDest, err := s.resolveDividendAccount(context.Background(),
		&store.DividendCandidate{AccountID: "buy-acct", UserID: "u1", UserKind: domain.KindClient, Currency: domain.CurrencyUSD},
		true)
	if err != nil {
		t.Fatal(err)
	}
	if acct != "buy-acct" || bankDest {
		t.Fatalf("got acct=%s bankDest=%v want buy-acct/false", acct, bankDest)
	}
}

func TestResolveDividendAccount_DefaultCurrency_S55(t *testing.T) {
	// Purchase account is gone → fall back to the holder's default
	// account in the security currency.
	bank := &stubDividendBank{
		accountErr: apperr.NotFound("account gone"),
		byCurrency: map[domain.Currency][]BankAccount{
			domain.CurrencyUSD: {{ID: "usd-default", Currency: domain.CurrencyUSD}},
		},
	}
	s := newDivService(bank)
	acct, _, err := s.resolveDividendAccount(context.Background(),
		&store.DividendCandidate{AccountID: "gone", UserID: "u1", UserKind: domain.KindClient, Currency: domain.CurrencyUSD},
		true)
	if err != nil {
		t.Fatal(err)
	}
	if acct != "usd-default" {
		t.Fatalf("got acct=%s want usd-default", acct)
	}
}

func TestResolveDividendAccount_RSDFallback_S56(t *testing.T) {
	// No account in the security currency → credit an RSD account (the
	// bank converts on credit).
	bank := &stubDividendBank{
		accountErr: apperr.NotFound("account gone"),
		byCurrency: map[domain.Currency][]BankAccount{
			domain.CurrencyUSD: nil,
			domain.CurrencyRSD: {{ID: "rsd-acct", Currency: domain.CurrencyRSD}},
		},
	}
	s := newDivService(bank)
	acct, _, err := s.resolveDividendAccount(context.Background(),
		&store.DividendCandidate{AccountID: "gone", UserID: "u1", UserKind: domain.KindClient, Currency: domain.CurrencyUSD},
		true)
	if err != nil {
		t.Fatal(err)
	}
	if acct != "rsd-acct" {
		t.Fatalf("got acct=%s want rsd-acct", acct)
	}
}

func TestResolveDividendAccount_NoAccount(t *testing.T) {
	// Holder has no usable account anywhere → FailedPrecondition.
	bank := &stubDividendBank{accountErr: apperr.NotFound("gone"), byCurrency: nil}
	s := newDivService(bank)
	_, _, err := s.resolveDividendAccount(context.Background(),
		&store.DividendCandidate{AccountID: "gone", UserID: "u1", UserKind: domain.KindClient, Currency: domain.CurrencyUSD},
		true)
	if err == nil {
		t.Fatal("expected an error when the holder has no account")
	}
}

func TestDividendRSD_NoRates(t *testing.T) {
	// Without a rate provider, a foreign dividend falls back to native.
	s := newDivService(nil)
	rsd := s.dividendRSD(context.Background(), money.MustParse("12.50"), domain.CurrencyUSD)
	if got := rsd.FloatString(2); got != "12.50" {
		t.Fatalf("rsd=%s want native 12.50 without rates", got)
	}
}

func TestDividendOpID_DeterministicPerQuarter(t *testing.T) {
	a := dividendOpID("h1", 2026, 2)
	b := dividendOpID("h1", 2026, 2)
	if a != b {
		t.Fatalf("op_id must be stable per (holding, quarter): %s != %s", a, b)
	}
	if a == dividendOpID("h1", 2026, 3) {
		t.Fatal("op_id must differ across quarters")
	}
	if a == dividendOpID("h2", 2026, 2) {
		t.Fatal("op_id must differ across holdings")
	}
}
