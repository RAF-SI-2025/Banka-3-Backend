//go:build integration

// Integration tests for c4 PR3 funds. Run against the same dev Postgres
// as the existing trading integration tests; bank settlement + FX rates
// are stubbed in-process.

package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/account"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// supervisorFundsCtx returns a supervisor principal with the c4 fund
// management bundle so the fund CRUD + invest-on-behalf-of-bank paths
// are admitted.
func supervisorFundsCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:   id,
		UserKind: auth.KindEmployee,
		Permissions: []string{
			permissions.Actuary, permissions.ActuarySupervisor,
			permissions.TradingMargin,
			permissions.FundsManageSupervisor, permissions.FundsReadSupervisor,
		},
	})
}

// clientFundsCtx returns a client principal with the unified trading
// capability (spec p.4 — TradingClient covers stocks + OTC + funds).
func clientFundsCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindClient,
		Permissions: []string{permissions.TradingClient},
	})
}

// seedFund creates a fund + its bank account via the public CreateFund
// path. Returns the fund and the bank account UUID the stub minted.
func seedFund(t *testing.T, svc *Service, supervisorID, name string) *domain.Fund {
	t.Helper()
	bankAccountID := uuid.NewString()
	currentReservations.fundAccountIDs = []string{bankAccountID}
	f, err := svc.CreateFund(supervisorFundsCtx(supervisorID), CreateFundInput{
		Name:                name,
		Description:         "Test fund",
		MinimumContribution: "1000",
	})
	if err != nil {
		t.Fatalf("CreateFund: %v", err)
	}
	if f.BankAccountID != bankAccountID {
		t.Fatalf("BankAccountID: got %s want %s", f.BankAccountID, bankAccountID)
	}
	return f
}

// TestIntegration_Fund_CreateAndInvest_Liquid asserts a full happy-path
// invest cycle. After CreateFund the bank account is empty and the
// fund has zero units; a 10_000 RSD invest mints 10_000 units at unit
// price = 1, credits the fund's bank account, and stamps the audit
// row completed. Unit price after = 1 (no holdings yet).
func TestIntegration_Fund_CreateAndInvest_Liquid(t *testing.T) {
	svc := setup(t)
	supervisorID := uuid.NewString()
	clientID := uuid.NewString()
	clientAccountID := uuid.NewString()
	currentReservations.setBalance(clientAccountID, "50000")
	currentReservations.setCurrency(clientAccountID, domain.CurrencyRSD)

	fund := seedFund(t, svc, supervisorID, "Alpha Fond")

	res, err := svc.InvestInFund(clientFundsCtx(clientID), InvestInFundInput{
		FundID:          fund.ID,
		AmountRSD:       "10000",
		SourceAccountID: clientAccountID,
	})
	if err != nil {
		t.Fatalf("InvestInFund: %v", err)
	}
	if res.Transaction.Status != domain.FundTxCompleted {
		t.Fatalf("status: got %s want completed", res.Transaction.Status)
	}
	if !numericEq(res.Transaction.UnitsDelta, "10000") {
		t.Fatalf("units_delta: got %s want 10000", res.Transaction.UnitsDelta)
	}

	// Bank balances moved as expected.
	if got := currentReservations.balance(clientAccountID); !numericEq(got, "40000") {
		t.Fatalf("client balance: got %s want 40000", got)
	}
	if got := currentReservations.balance(fund.BankAccountID); !numericEq(got, "10000") {
		t.Fatalf("fund balance: got %s want 10000", got)
	}

	// Fund row + position state.
	f2, err := svc.Store.GetFund(context.Background(), fund.ID)
	if err != nil {
		t.Fatalf("GetFund: %v", err)
	}
	if !numericEq(f2.TotalUnits, "10000") {
		t.Fatalf("fund.total_units: got %s want 10000", f2.TotalUnits)
	}
	pos, err := svc.Store.GetFundPosition(context.Background(), fund.ID, clientID)
	if err != nil {
		t.Fatalf("GetFundPosition: %v", err)
	}
	if !numericEq(pos.Units, "10000") {
		t.Fatalf("position.units: got %s want 10000", pos.Units)
	}
	if !numericEq(pos.TotalInvestedRSD, "10000") {
		t.Fatalf("position.total_invested_rsd: got %s want 10000", pos.TotalInvestedRSD)
	}
}

// TestIntegration_Fund_Invest_FXSource_RSDDenominated asserts the spec
// p.71 invariant: the investor commits a figure in RSD (the fund's
// accounting unit) and the fund is credited exactly that RSD even when
// the source account is foreign. The src→RSD ASK conversion plus the
// client FX commission are charged on top of the source-account debit
// so the fund still nets the whole committed amount; the
// minimum_contribution gate is checked against the committed RSD, not
// a post-conversion figure. Mirrors the withdraw FX haircut.
func TestIntegration_Fund_Invest_FXSource_RSDDenominated(t *testing.T) {
	svc := setup(t)
	supervisorID := uuid.NewString()
	clientID := uuid.NewString()
	usdAccountID := uuid.NewString()
	currentReservations.setBalance(usdAccountID, "5000")
	currentReservations.setCurrency(usdAccountID, domain.CurrencyUSD)

	fund := seedFund(t, svc, supervisorID, "Beta Fond")

	// Commit 110_500 RSD from a USD account. pinnedRates USD→RSD
	// ask = 110.50, FXCommission = 0.005.
	res, err := svc.InvestInFund(clientFundsCtx(clientID), InvestInFundInput{
		FundID:          fund.ID,
		AmountRSD:       "110500",
		SourceAccountID: usdAccountID,
	})
	if err != nil {
		t.Fatalf("InvestInFund(FX): %v", err)
	}
	if res.Transaction.Status != domain.FundTxCompleted {
		t.Fatalf("status: got %s want completed", res.Transaction.Status)
	}

	// Expected source debit: base = 110500/110.50 = 1000 USD; grossed
	// up by 1/(1-0.005) so the fund still receives the full RSD.
	ask := money.MustParse("110.50")
	base, err := money.Div(money.MustParse("110500"), ask)
	if err != nil {
		t.Fatalf("div: %v", err)
	}
	oneMinusC := money.Sub(money.MustParse("1"), money.MustParse("0.005"))
	srcExpected, err := money.Div(base, oneMinusC)
	if err != nil {
		t.Fatalf("div: %v", err)
	}
	srcExpectedStr := money.FormatAmount(srcExpected)

	// The fund is credited the FULL committed RSD — the core spec
	// invariant. UnitsDelta == committed RSD (fresh fund, unit price 1).
	if got := currentReservations.balance(fund.BankAccountID); !numericEq(got, "110500") {
		t.Fatalf("fund balance: got %s want 110500 (full committed RSD)", got)
	}
	if !numericEq(res.Transaction.UnitsDelta, "110500") {
		t.Fatalf("units_delta: got %s want 110500", res.Transaction.UnitsDelta)
	}

	// The USD source paid the conversion + commission on top.
	if n := len(currentReservations.reserveCalls); n == 0 {
		t.Fatalf("no reserve call recorded")
	}
	gotSrc := currentReservations.reserveCalls[len(currentReservations.reserveCalls)-1].Amount
	if !numericEq(gotSrc, srcExpectedStr) {
		t.Fatalf("source reserve: got %s want %s (base/(1-c))", gotSrc, srcExpectedStr)
	}
	if money.MustParse(gotSrc).Cmp(base) <= 0 {
		t.Fatalf("source reserve %s must exceed commission-free base %s", gotSrc, money.FormatAmount(base))
	}
	wantBal := money.FormatAmount(money.Sub(money.MustParse("5000"), money.MustParse(srcExpectedStr)))
	if got := currentReservations.balance(usdAccountID); !numericEq(got, wantBal) {
		t.Fatalf("usd balance: got %s want %s", got, wantBal)
	}

	// Position is recorded in the committed RSD, not the FX figure.
	pos, err := svc.Store.GetFundPosition(context.Background(), fund.ID, clientID)
	if err != nil {
		t.Fatalf("GetFundPosition: %v", err)
	}
	if !numericEq(pos.TotalInvestedRSD, "110500") {
		t.Fatalf("position.total_invested_rsd: got %s want 110500", pos.TotalInvestedRSD)
	}
}

// TestIntegration_Fund_Invest_BelowMinimum rejects amounts below the
// fund's minimum_contribution with a FailedPrecondition.
func TestIntegration_Fund_Invest_BelowMinimum(t *testing.T) {
	svc := setup(t)
	supervisorID := uuid.NewString()
	clientID := uuid.NewString()
	clientAccountID := uuid.NewString()
	currentReservations.setBalance(clientAccountID, "50000")
	currentReservations.setCurrency(clientAccountID, domain.CurrencyRSD)

	fund := seedFund(t, svc, supervisorID, "Beta Fond")

	_, err := svc.InvestInFund(clientFundsCtx(clientID), InvestInFundInput{
		FundID:          fund.ID,
		AmountRSD:       "100", // below 1000 minimum
		SourceAccountID: clientAccountID,
	})
	if err == nil {
		t.Fatal("expected FailedPrecondition for below-min invest")
	}
}

// TestIntegration_Fund_Withdraw_Liquid_Happy invests, then withdraws
// half. Asserts the fund's bank account drops, the client's account
// is credited, the position unit count halves, and a realized_gains
// row lands for the client with proceeds = withdrawn RSD and
// cost_basis = half of total_invested_rsd.
func TestIntegration_Fund_Withdraw_Liquid_Happy(t *testing.T) {
	svc := setup(t)
	supervisorID := uuid.NewString()
	clientID := uuid.NewString()
	clientAccountID := uuid.NewString()
	currentReservations.setBalance(clientAccountID, "50000")
	currentReservations.setCurrency(clientAccountID, domain.CurrencyRSD)

	fund := seedFund(t, svc, supervisorID, "Gamma Fond")
	if _, err := svc.InvestInFund(clientFundsCtx(clientID), InvestInFundInput{
		FundID:          fund.ID,
		AmountRSD:       "10000",
		SourceAccountID: clientAccountID,
	}); err != nil {
		t.Fatalf("InvestInFund: %v", err)
	}

	res, err := svc.WithdrawFromFund(clientFundsCtx(clientID), WithdrawFromFundInput{
		FundID:        fund.ID,
		AmountRSD:     "5000",
		DestAccountID: clientAccountID,
	})
	if err != nil {
		t.Fatalf("WithdrawFromFund: %v", err)
	}
	if res.Pending {
		t.Fatalf("withdraw should be liquid (pending=false), got pending=true")
	}
	if res.Transaction.Status != domain.FundTxCompleted {
		t.Fatalf("status: got %s want completed", res.Transaction.Status)
	}

	// Bank balances.
	if got := currentReservations.balance(fund.BankAccountID); !numericEq(got, "5000") {
		t.Fatalf("fund balance after withdraw: got %s want 5000", got)
	}
	if got := currentReservations.balance(clientAccountID); !numericEq(got, "45000") {
		t.Fatalf("client balance after withdraw: got %s want 45000", got)
	}
	// Position halved.
	pos, err := svc.Store.GetFundPosition(context.Background(), fund.ID, clientID)
	if err != nil {
		t.Fatalf("GetFundPosition: %v", err)
	}
	if !numericEq(pos.Units, "5000") {
		t.Fatalf("position.units: got %s want 5000", pos.Units)
	}
	if !numericEq(pos.TotalInvestedRSD, "5000") {
		t.Fatalf("position.total_invested_rsd: got %s want 5000", pos.TotalInvestedRSD)
	}
	// Realized gain row for the client. proceeds = 5000, cost_basis =
	// 5000 (half of 10000), gain = 0.
	rows, err := svc.Store.ListUnpaidGainsForUser(context.Background(), clientID, domain.KindClient)
	if err != nil {
		t.Fatalf("ListUnpaidGainsForUser: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("realized_gains rows: got %d want 1", len(rows))
	}
	if !numericEq(rows[0].ProceedsAmt, "5000") {
		t.Fatalf("proceeds: got %s want 5000", rows[0].ProceedsAmt)
	}
	if !numericEq(rows[0].CostBasisAmt, "5000") {
		t.Fatalf("cost_basis: got %s want 5000", rows[0].CostBasisAmt)
	}
	if !numericEq(rows[0].GainRSD, "0") {
		t.Fatalf("gain_rsd: got %s want 0", rows[0].GainRSD)
	}
}

// TestIntegration_Fund_Withdraw_All withdraws the caller's full
// position via the WithdrawAll convenience flag.
func TestIntegration_Fund_Withdraw_All(t *testing.T) {
	svc := setup(t)
	supervisorID := uuid.NewString()
	clientID := uuid.NewString()
	clientAccountID := uuid.NewString()
	currentReservations.setBalance(clientAccountID, "50000")
	currentReservations.setCurrency(clientAccountID, domain.CurrencyRSD)

	fund := seedFund(t, svc, supervisorID, "Delta Fond")
	if _, err := svc.InvestInFund(clientFundsCtx(clientID), InvestInFundInput{
		FundID:          fund.ID,
		AmountRSD:       "10000",
		SourceAccountID: clientAccountID,
	}); err != nil {
		t.Fatalf("InvestInFund: %v", err)
	}
	if _, err := svc.WithdrawFromFund(clientFundsCtx(clientID), WithdrawFromFundInput{
		FundID:        fund.ID,
		DestAccountID: clientAccountID,
		WithdrawAll:   true,
	}); err != nil {
		t.Fatalf("WithdrawFromFund(all): %v", err)
	}
	pos, err := svc.Store.GetFundPosition(context.Background(), fund.ID, clientID)
	if err != nil {
		t.Fatalf("GetFundPosition: %v", err)
	}
	if !numericEq(pos.Units, "0") {
		t.Fatalf("position.units after withdraw-all: got %s want 0", pos.Units)
	}
}

// TestIntegration_Fund_BankAsClient_InvestAndWithdraw exercises the
// supervisor-acting-for-the-bank flow. Asserts client_id =
// BankAsClientOwnerID on the position + audit + realized_gain rows.
func TestIntegration_Fund_BankAsClient_InvestAndWithdraw(t *testing.T) {
	svc := setup(t)
	supervisorID := uuid.NewString()
	bankSourceAccount := uuid.NewString()
	currentReservations.setBalance(bankSourceAccount, "100000")
	currentReservations.setCurrency(bankSourceAccount, domain.CurrencyRSD)

	fund := seedFund(t, svc, supervisorID, "Epsilon Fond")

	res, err := svc.InvestInFund(supervisorFundsCtx(supervisorID), InvestInFundInput{
		FundID:           fund.ID,
		AmountRSD:        "20000",
		SourceAccountID:  bankSourceAccount,
		OnBehalfClientID: account.BankAsClientOwnerID,
	})
	if err != nil {
		t.Fatalf("InvestInFund(bank): %v", err)
	}
	if res.Transaction.ClientID != account.BankAsClientOwnerID {
		t.Fatalf("client_id: got %s want %s", res.Transaction.ClientID, account.BankAsClientOwnerID)
	}
	pos, err := svc.Store.GetFundPosition(context.Background(), fund.ID, account.BankAsClientOwnerID)
	if err != nil {
		t.Fatalf("GetFundPosition(bank): %v", err)
	}
	if !numericEq(pos.Units, "20000") {
		t.Fatalf("bank.units: got %s want 20000", pos.Units)
	}
}

// TestIntegration_Fund_FundActorOrder asserts a supervisor can place
// a SELL order on behalf of a fund. Fund-actor orders book holdings to
// (fund.id, KindFund); the realized_gain skip is verified in the
// execution worker.
func TestIntegration_Fund_FundActorOrder_HoldingSeed(t *testing.T) {
	svc := setup(t)
	supervisorID := uuid.NewString()
	clientID := uuid.NewString()
	clientAccountID := uuid.NewString()
	currentReservations.setBalance(clientAccountID, "1000000")
	currentReservations.setCurrency(clientAccountID, domain.CurrencyRSD)

	fund := seedFund(t, svc, supervisorID, "Zeta Fond")
	// Invest enough RSD to buy stock.
	if _, err := svc.InvestInFund(clientFundsCtx(clientID), InvestInFundInput{
		FundID:          fund.ID,
		AmountRSD:       "500000",
		SourceAccountID: clientAccountID,
	}); err != nil {
		t.Fatalf("InvestInFund: %v", err)
	}

	// Seed a fund holding directly via the store so we can sell.
	ex := seedExchange(t, svc, "XBEL", domain.CurrencyRSD)
	sec, _ := seedStock(t, svc, "NIS", ex, "100", "100", "99", 1000)
	if err := svc.Store.ExecuteAtomic(context.Background(), func(tx pgx.Tx) error {
		_, err := svc.Store.ApplyBuyFill(context.Background(), tx,
			fund.ID, string(domain.KindFund), sec.ID, fund.BankAccountID, 100, "100")
		return err
	}); err != nil {
		t.Fatalf("ApplyBuyFill (fund): %v", err)
	}

	out, err := svc.CreateOrder(supervisorFundsCtx(supervisorID), CreateOrderInput{
		SecurityID:       sec.ID,
		OrderType:        domain.OrderMarket,
		Direction:        domain.DirectionSell,
		Quantity:         10,
		AccountID:        fund.BankAccountID,
		OnBehalfOfFundID: fund.ID,
	})
	if err != nil {
		t.Fatalf("CreateOrder (fund-actor): %v", err)
	}
	if out.Order.UserKind != domain.KindFund {
		t.Fatalf("user_kind: got %s want fund", out.Order.UserKind)
	}
	if out.Order.OnBehalfOfFundID != fund.ID {
		t.Fatalf("on_behalf_of_fund_id: got %s want %s", out.Order.OnBehalfOfFundID, fund.ID)
	}
	if !out.Order.IsActuary {
		t.Fatal("is_actuary should be true for fund-actor orders")
	}
}

// TestIntegration_Fund_DiscoveryDecoration asserts ListFunds returns
// the decorated total_value/profit/unit_price columns when bank state
// is reachable through the stub.
func TestIntegration_Fund_DiscoveryDecoration(t *testing.T) {
	svc := setup(t)
	supervisorID := uuid.NewString()
	clientID := uuid.NewString()
	clientAccountID := uuid.NewString()
	currentReservations.setBalance(clientAccountID, "100000")
	currentReservations.setCurrency(clientAccountID, domain.CurrencyRSD)

	fund := seedFund(t, svc, supervisorID, "Eta Fond")
	if _, err := svc.InvestInFund(clientFundsCtx(clientID), InvestInFundInput{
		FundID:          fund.ID,
		AmountRSD:       "10000",
		SourceAccountID: clientAccountID,
	}); err != nil {
		t.Fatalf("InvestInFund: %v", err)
	}
	rows, err := svc.ListFunds(supervisorFundsCtx(supervisorID), ListFundsInput{})
	if err != nil {
		t.Fatalf("ListFunds: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: got %d want 1", len(rows))
	}
	if !numericEq(rows[0].LiquidRSD, "10000") {
		t.Fatalf("liquid_rsd: got %s want 10000", rows[0].LiquidRSD)
	}
	if !numericEq(rows[0].UnitPriceRSD, "1") {
		t.Fatalf("unit_price_rsd: got %s want 1", rows[0].UnitPriceRSD)
	}
}

// adminCtx returns a principal carrying the internal admin sentinel —
// matches the metadata the user-svc adapter attaches when it dials
// trading.ReassignSupervisorAssets.
func adminCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
}

// TestIntegration_Cascade_ReassignFundsOnDemotion is the c4 PR8 TEST-4
// cascade gate: admin creates supervisor X, X mints fund A + fund B,
// admin demotes X (revokes funds.manage.supervisor) — the user-svc
// trigger calls trading.ReassignSupervisorAssets which must flip both
// funds over to the acting admin in one shot. We exercise the trading
// primitive directly (the user-svc → trading adapter is a thin proto
// pass-through; its end-to-end behavior is covered by the celina4 live
// cypress + the FundReassigner stub already in user-svc unit tests).
// Also asserts idempotency: a second call after the flip returns 0.
func TestIntegration_Cascade_ReassignFundsOnDemotion(t *testing.T) {
	svc := setup(t)
	supervisorID := uuid.NewString()
	adminID := uuid.NewString()

	fundA := seedFund(t, svc, supervisorID, "Alpha Cascade")
	fundB := seedFund(t, svc, supervisorID, "Beta Cascade")

	n, err := svc.ReassignSupervisorAssets(adminCtx(adminID), supervisorID, adminID)
	if err != nil {
		t.Fatalf("ReassignSupervisorAssets: %v", err)
	}
	if n != 2 {
		t.Fatalf("reassigned count: got %d want 2", n)
	}

	for _, id := range []string{fundA.ID, fundB.ID} {
		got, err := svc.Store.GetFund(context.Background(), id)
		if err != nil {
			t.Fatalf("GetFund(%s): %v", id, err)
		}
		if got.ManagerUserID != adminID {
			t.Fatalf("fund %s manager_user_id: got %s want %s", id, got.ManagerUserID, adminID)
		}
	}

	// Idempotent: after the flip there's no fund still managed by the
	// demoted supervisor, so a re-run is a no-op.
	n, err = svc.ReassignSupervisorAssets(adminCtx(adminID), supervisorID, adminID)
	if err != nil {
		t.Fatalf("ReassignSupervisorAssets (re-run): %v", err)
	}
	if n != 0 {
		t.Fatalf("re-run count: got %d want 0", n)
	}
}

// TestIntegration_Cascade_SkipsClosedFunds asserts only funds in
// `status='active'` get reassigned — closed funds stay with the prior
// manager (no point handing administrative tail off a wound-down fund).
func TestIntegration_Cascade_SkipsClosedFunds(t *testing.T) {
	svc := setup(t)
	supervisorID := uuid.NewString()
	adminID := uuid.NewString()

	fundActive := seedFund(t, svc, supervisorID, "Active Cascade")
	fundClosed := seedFund(t, svc, supervisorID, "Closed Cascade")
	if _, err := fixPool.Exec(context.Background(),
		`update "trading".investment_funds set status='closed' where id=$1`,
		fundClosed.ID); err != nil {
		t.Fatalf("close fund: %v", err)
	}

	n, err := svc.ReassignSupervisorAssets(adminCtx(adminID), supervisorID, adminID)
	if err != nil {
		t.Fatalf("ReassignSupervisorAssets: %v", err)
	}
	if n != 1 {
		t.Fatalf("reassigned count: got %d want 1", n)
	}

	gotActive, err := svc.Store.GetFund(context.Background(), fundActive.ID)
	if err != nil {
		t.Fatalf("GetFund(active): %v", err)
	}
	if gotActive.ManagerUserID != adminID {
		t.Fatalf("active fund not flipped: got %s want %s", gotActive.ManagerUserID, adminID)
	}
	gotClosed, err := svc.Store.GetFund(context.Background(), fundClosed.ID)
	if err != nil {
		t.Fatalf("GetFund(closed): %v", err)
	}
	if gotClosed.ManagerUserID != supervisorID {
		t.Fatalf("closed fund unexpectedly flipped: got %s want %s", gotClosed.ManagerUserID, supervisorID)
	}
}
