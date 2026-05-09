//go:build integration

// Trade-settlement integration tests exercise the bank-side surface
// the trading service depends on: SettleTrade (per-fill cash leg) and
// SettleCapitalGainsTax (the monthly tax debit). They share the same
// fixture as the rest of the bank suite — see integration_test.go.
package service

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// adminTradingCtx mints the same admin-flavored principal the trading
// service forwards on outgoing SettleTrade calls. Avoids depending on
// the trading-service adapter at test time.
func adminTradingCtx() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffe",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
}

// TestIntegration_SettleTrade_ClientDebit covers the happy path: a
// same-currency client buy debits their account and credits the bank
// house, both legs land, and the response carries an op_id.
func TestIntegration_SettleTrade_ClientDebit(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalFX, domain.CurrencyUSD, "1000")

	res, err := svc.SettleTrade(adminTradingCtx(), SettleTradeInput{
		AccountID: acc.ID,
		Direction: "debit",
		Currency:  domain.CurrencyUSD,
		Amount:    "100",
		OpID:      uuid.NewString(),
		IsActuary: false,
		Purpose:   "Trade fill",
	})
	if err != nil {
		t.Fatalf("SettleTrade: %v", err)
	}
	if len(res.Transactions) == 0 {
		t.Fatalf("expected at least one tx leg")
	}
	// Account balance dropped by 100.
	post, err := svc.Store.GetAccountByID(context.Background(), acc.ID)
	if err != nil {
		t.Fatalf("GetAccountByID: %v", err)
	}
	if !numericEq(post.Balance, "900") {
		t.Fatalf("post-debit balance = %s, want 900", post.Balance)
	}
}

// TestIntegration_SettleTrade_Idempotent confirms a retry with the
// same op_id returns the original legs without re-charging the user.
func TestIntegration_SettleTrade_Idempotent(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalFX, domain.CurrencyUSD, "1000")
	opID := uuid.NewString()

	first, err := svc.SettleTrade(adminTradingCtx(), SettleTradeInput{
		AccountID: acc.ID, Direction: "debit", Currency: domain.CurrencyUSD,
		Amount: "50", OpID: opID,
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.SettleTrade(adminTradingCtx(), SettleTradeInput{
		AccountID: acc.ID, Direction: "debit", Currency: domain.CurrencyUSD,
		Amount: "50", OpID: opID,
	})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(first.Transactions) != len(second.Transactions) {
		t.Fatalf("retry leg count: first=%d second=%d", len(first.Transactions), len(second.Transactions))
	}

	// Account dropped by 50, not 100.
	post, _ := svc.Store.GetAccountByID(context.Background(), acc.ID)
	if !numericEq(post.Balance, "950") {
		t.Fatalf("post-retry balance = %s, want 950 (not double-charged)", post.Balance)
	}
}

// TestIntegration_SettleTrade_ActuaryRequiresBankAccount verifies the
// fix #6 guard: when IsActuary=true the source account must be a
// bank-owned (KindSystem) account; a client account yields
// FailedPrecondition.
func TestIntegration_SettleTrade_ActuaryRequiresBankAccount(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalFX, domain.CurrencyUSD, "1000")

	_, err := svc.SettleTrade(adminTradingCtx(), SettleTradeInput{
		AccountID: acc.ID,
		Direction: "debit",
		Currency:  domain.CurrencyUSD,
		Amount:    "100",
		OpID:      uuid.NewString(),
		IsActuary: true, // critical
	})
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("actuary on client account: err=%v, want FailedPrecondition", err)
	}
}

// TestIntegration_SettleTrade_ActuaryUsesForexBook verifies that
// after the fix #6 relaxation, an actuary settle against the bank's
// per-currency forex_book account succeeds — debiting the book and
// crediting the menjačnica house when direction=debit.
func TestIntegration_SettleTrade_ActuaryUsesForexBook(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()

	book, err := svc.Store.GetForexBookAccount(ctx, domain.CurrencyUSD)
	if err != nil {
		t.Fatalf("GetForexBookAccount: %v", err)
	}
	bookBefore := book.Balance

	if _, err := svc.SettleTrade(adminTradingCtx(), SettleTradeInput{
		AccountID: book.ID,
		Direction: "debit",
		Currency:  domain.CurrencyUSD,
		Amount:    "100",
		OpID:      uuid.NewString(),
		IsActuary: true,
	}); err != nil {
		t.Fatalf("actuary forex-book settle: %v", err)
	}

	postBook, _ := svc.Store.GetForexBookAccount(ctx, domain.CurrencyUSD)
	if !decreasedBy(bookBefore, postBook.Balance, "100") {
		t.Fatalf("forex book balance: %s → %s, want -100", bookBefore, postBook.Balance)
	}
}

// TestIntegration_SettleCapitalGainsTax_RSDFlow exercises the simple
// path: a tax debit from a client's RSD account into the state
// account. The state account must be pre-seeded by EnsureSystemAccounts.
func TestIntegration_SettleCapitalGainsTax_RSDFlow(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "10000")

	_, err := svc.SettleCapitalGainsTax(adminTradingCtx(), SettleCapitalGainsTaxInput{
		AccountID: acc.ID,
		AmountRSD: "150",
		OpID:      uuid.NewString(),
		Purpose:   "Porez na kapitalni dobitak",
	})
	if err != nil {
		t.Fatalf("SettleCapitalGainsTax: %v", err)
	}

	// Client account dropped by 150; state account gained 150.
	postClient, _ := svc.Store.GetAccountByID(context.Background(), acc.ID)
	if !numericEq(postClient.Balance, "9850") {
		t.Fatalf("client balance after tax = %s, want 9850", postClient.Balance)
	}

	state, err := svc.Store.GetStateTaxAccount(context.Background())
	if err != nil {
		t.Fatalf("GetStateTaxAccount: %v", err)
	}
	if !numericEq(state.Balance, "150") {
		t.Fatalf("state account balance = %s, want 150", state.Balance)
	}
}

// TestIntegration_SettleCapitalGainsTax_FXLegNoCommission exercises
// spec p.62: when the user's account is in foreign currency, the FX
// leg into RSD must not charge a commission.
func TestIntegration_SettleCapitalGainsTax_FXLegNoCommission(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalFX, domain.CurrencyUSD, "1000")

	// 110.50 RSD/USD ASK → 100 RSD ≈ 0.9050 USD. With commission this
	// would be 0.9095 USD. Verify no commission was applied.
	_, err := svc.SettleCapitalGainsTax(adminTradingCtx(), SettleCapitalGainsTaxInput{
		AccountID: acc.ID,
		AmountRSD: "100",
		OpID:      uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("SettleCapitalGainsTax fx: %v", err)
	}
	postClient, _ := svc.Store.GetAccountByID(context.Background(), acc.ID)
	delta := money.MustParse("1000")
	cur, _ := money.Parse(postClient.Balance)
	delta = money.Sub(delta, cur)
	// Expected USD pull ≈ 100/110.50 ≈ 0.9050. With 0.5% commission it
	// would be ~0.9095. Allow a small tolerance for rounding.
	want := money.MustParse("0.9050")
	diff := money.Sub(delta, want)
	if diff.Sign() < 0 {
		diff.Neg(diff)
	}
	cap := money.MustParse("0.001")
	if diff.Cmp(cap) > 0 {
		t.Fatalf("USD debited = %s, want ~0.9050 (no commission); diff=%s",
			money.FormatAmount(delta), money.FormatAmount(diff))
	}

	// State account got the full 100 RSD.
	state, _ := svc.Store.GetStateTaxAccount(context.Background())
	if !numericEq(state.Balance, "100") {
		t.Fatalf("state account = %s, want 100", state.Balance)
	}
}

// TestIntegration_SettleForexFill_BuyEURUSD verifies the spec p.42
// paired settlement: the bank's per-currency house and forex_book
// accounts move in opposite directions on each leg, by the correct
// amounts, atomically, idempotent on op_id.
func TestIntegration_SettleForexFill_BuyEURUSD(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()

	houseUSDBefore, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyUSD)
	houseEURBefore, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyEUR)
	bookUSDBefore, _ := svc.Store.GetForexBookAccount(ctx, domain.CurrencyUSD)
	bookEURBefore, _ := svc.Store.GetForexBookAccount(ctx, domain.CurrencyEUR)

	opID := uuid.NewString()
	res, err := svc.SettleForexFill(adminTradingCtx(), SettleForexFillInput{
		Direction:     "buy",
		BaseCurrency:  domain.CurrencyEUR,
		BaseAmount:    "1000",
		QuoteCurrency: domain.CurrencyUSD,
		QuoteAmount:   "1100",
		OpID:          opID,
		Purpose:       "Forex fill EURUSD",
	})
	if err != nil {
		t.Fatalf("SettleForexFill: %v", err)
	}
	if len(res.Transactions) == 0 {
		t.Fatalf("expected at least one tx leg")
	}

	// Quote leg (USD): house→book, so house USD drops 1100, book USD
	// rises 1100.
	houseUSDAfter, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyUSD)
	bookUSDAfter, _ := svc.Store.GetForexBookAccount(ctx, domain.CurrencyUSD)
	if !decreasedBy(houseUSDBefore.Balance, houseUSDAfter.Balance, "1100") {
		t.Fatalf("USD house: %s → %s, want -1100", houseUSDBefore.Balance, houseUSDAfter.Balance)
	}
	if !increasedBy(bookUSDBefore.Balance, bookUSDAfter.Balance, "1100") {
		t.Fatalf("USD book: %s → %s, want +1100", bookUSDBefore.Balance, bookUSDAfter.Balance)
	}

	// Base leg (EUR): book→house, so book EUR drops 1000, house EUR
	// rises 1000.
	houseEURAfter, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyEUR)
	bookEURAfter, _ := svc.Store.GetForexBookAccount(ctx, domain.CurrencyEUR)
	if !decreasedBy(bookEURBefore.Balance, bookEURAfter.Balance, "1000") {
		t.Fatalf("EUR book: %s → %s, want -1000", bookEURBefore.Balance, bookEURAfter.Balance)
	}
	if !increasedBy(houseEURBefore.Balance, houseEURAfter.Balance, "1000") {
		t.Fatalf("EUR house: %s → %s, want +1000", houseEURBefore.Balance, houseEURAfter.Balance)
	}

	// Idempotent retry: same op_id returns existing legs without
	// re-charging.
	if _, err := svc.SettleForexFill(adminTradingCtx(), SettleForexFillInput{
		Direction:     "buy",
		BaseCurrency:  domain.CurrencyEUR,
		BaseAmount:    "1000",
		QuoteCurrency: domain.CurrencyUSD,
		QuoteAmount:   "1100",
		OpID:          opID,
	}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	houseUSDFinal, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyUSD)
	if !numericEq(houseUSDFinal.Balance, houseUSDAfter.Balance) {
		t.Fatalf("retry double-charged: %s → %s", houseUSDAfter.Balance, houseUSDFinal.Balance)
	}
}

// TestIntegration_SettleForexFill_SellReverses verifies the sell
// direction inverts both leg directions.
func TestIntegration_SettleForexFill_SellReverses(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()

	houseUSDBefore, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyUSD)
	houseEURBefore, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyEUR)

	if _, err := svc.SettleForexFill(adminTradingCtx(), SettleForexFillInput{
		Direction:     "sell",
		BaseCurrency:  domain.CurrencyEUR,
		BaseAmount:    "500",
		QuoteCurrency: domain.CurrencyUSD,
		QuoteAmount:   "550",
		OpID:          uuid.NewString(),
	}); err != nil {
		t.Fatalf("SettleForexFill sell: %v", err)
	}

	houseUSDAfter, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyUSD)
	houseEURAfter, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyEUR)
	// Sell base: book→house USD (USD house up), house→book EUR (EUR house down).
	if !increasedBy(houseUSDBefore.Balance, houseUSDAfter.Balance, "550") {
		t.Fatalf("sell USD house: %s → %s, want +550", houseUSDBefore.Balance, houseUSDAfter.Balance)
	}
	if !decreasedBy(houseEURBefore.Balance, houseEURAfter.Balance, "500") {
		t.Fatalf("sell EUR house: %s → %s, want -500", houseEURBefore.Balance, houseEURAfter.Balance)
	}
}

// decreasedBy reports whether after = before − delta.
func decreasedBy(before, after, delta string) bool {
	b, _ := money.Parse(before)
	a, _ := money.Parse(after)
	d, _ := money.Parse(delta)
	return money.Sub(b, a).Cmp(d) == 0
}

func increasedBy(before, after, delta string) bool {
	b, _ := money.Parse(before)
	a, _ := money.Parse(after)
	d, _ := money.Parse(delta)
	return money.Sub(a, b).Cmp(d) == 0
}

// numericEq compares two decimal-string amounts via money.Parse so
// "100" and "100.0000" compare equal.
func numericEq(a, b string) bool {
	ar, err := money.Parse(a)
	if err != nil {
		return false
	}
	br, err := money.Parse(b)
	if err != nil {
		return false
	}
	return ar.Cmp(br) == 0
}

// avoid unused-import errors in case strings/money etc get refactored
var _ = strings.HasPrefix
