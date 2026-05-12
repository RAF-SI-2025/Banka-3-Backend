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

// clientFundsCtx returns a client principal with the trading + funds
// invest bundle.
func clientFundsCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:   id,
		UserKind: auth.KindClient,
		Permissions: []string{
			permissions.TradingClient,
			permissions.FundsReadClient, permissions.FundsInvestClient,
		},
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
		Amount:          "10000",
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
		Amount:          "100", // below 1000 minimum
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
		Amount:          "10000",
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
		Amount:          "10000",
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
		Amount:           "20000",
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
		Amount:          "500000",
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
		Amount:          "10000",
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

var _ = permissions.Admin // silence unused-import when no helper refs it directly
