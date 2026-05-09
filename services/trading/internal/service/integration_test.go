//go:build integration

// Package service integration tests exercise the trading service
// against a real Postgres. Same gating + reset pattern as the bank
// service:
//
//	task up CELINA=c2          # bring up postgres
//	task migrate               # apply trading schema
//	task test:integration      # runs this suite
//
// Bank settlement, FX rates, and bank-account reads are stubbed
// in-process so we don't need a live `bank` or `exchange` container.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
)

// numericEq compares two decimal-string amounts via money.Parse so
// "95" and "95.0000" compare equal.
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

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

func isApperr(err error, kind apperr.Kind) bool {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Kind == kind
}

// =====================================================================
// Shared fixture
// =====================================================================

var (
	fixOnce sync.Once
	fixPool *pgxpool.Pool
	fixSkip string
)

// pinnedRates is a deterministic RateProvider stub. The figures match
// the c2 worked examples + bank integration suite.
type pinnedRates struct{}

func (pinnedRates) Quote(_ context.Context, from, to domain.Currency) (string, string, error) {
	switch {
	case from == to:
		return "1", "1", nil
	case from == domain.CurrencyEUR && to == domain.CurrencyRSD:
		return "117.20", "117.50", nil
	case from == domain.CurrencyUSD && to == domain.CurrencyRSD:
		return "110.20", "110.50", nil
	case from == domain.CurrencyCHF && to == domain.CurrencyRSD:
		return "118.00", "118.50", nil
	case from == domain.CurrencyGBP && to == domain.CurrencyRSD:
		return "138.00", "138.40", nil
	}
	return "", "", fmt.Errorf("no pinned rate for %s/%s", from, to)
}

// intStubSettler captures every Settle call and returns the requested op
// id back unchanged. The tax-settler half satisfies TaxSettler too.
type intStubSettler struct {
	sync.Mutex
	settleCalls []SettleInput
	taxCalls    []TaxSettleInput
	forexCalls  []SettleForexInput
	settleErr   error
	taxErr      error
	forexErr    error
}

func (s *intStubSettler) Settle(_ context.Context, in SettleInput) (string, error) {
	s.Lock()
	defer s.Unlock()
	if s.settleErr != nil {
		return "", s.settleErr
	}
	s.settleCalls = append(s.settleCalls, in)
	return in.OpID, nil
}

func (s *intStubSettler) SettleTax(_ context.Context, in TaxSettleInput) (string, error) {
	s.Lock()
	defer s.Unlock()
	if s.taxErr != nil {
		return "", s.taxErr
	}
	s.taxCalls = append(s.taxCalls, in)
	return in.OpID, nil
}

func (s *intStubSettler) SettleForex(_ context.Context, in SettleForexInput) (string, error) {
	s.Lock()
	defer s.Unlock()
	if s.forexErr != nil {
		return "", s.forexErr
	}
	s.forexCalls = append(s.forexCalls, in)
	return in.OpID, nil
}

func (s *intStubSettler) reset() {
	s.Lock()
	defer s.Unlock()
	s.settleCalls = s.settleCalls[:0]
	s.taxCalls = s.taxCalls[:0]
	s.forexCalls = s.forexCalls[:0]
	s.settleErr = nil
	s.taxErr = nil
	s.forexErr = nil
}

func (s *intStubSettler) settles() []SettleInput {
	s.Lock()
	defer s.Unlock()
	out := make([]SettleInput, len(s.settleCalls))
	copy(out, s.settleCalls)
	return out
}

// stubMargin satisfies MarginChecker. Use Add() to seed expected
// account balances and loans for a given client.
type stubMargin struct {
	sync.Mutex
	accounts map[string]struct {
		cur  domain.Currency
		amt  string
	}
	loans map[string]struct {
		cur domain.Currency
		amt string
	}
}

func newStubMargin() *stubMargin {
	return &stubMargin{
		accounts: map[string]struct {
			cur domain.Currency
			amt string
		}{},
		loans: map[string]struct {
			cur domain.Currency
			amt string
		}{},
	}
}

func (s *stubMargin) AccountAvailable(_ context.Context, accountID string) (domain.Currency, string, error) {
	s.Lock()
	defer s.Unlock()
	if v, ok := s.accounts[accountID]; ok {
		return v.cur, v.amt, nil
	}
	return "", "", fmt.Errorf("no stub account %q", accountID)
}

func (s *stubMargin) ClientLargestActiveLoan(_ context.Context, clientID string) (domain.Currency, string, error) {
	s.Lock()
	defer s.Unlock()
	if v, ok := s.loans[clientID]; ok {
		return v.cur, v.amt, nil
	}
	return "", "", nil
}

func (s *stubMargin) addAccount(id string, cur domain.Currency, amt string) {
	s.Lock()
	defer s.Unlock()
	s.accounts[id] = struct {
		cur domain.Currency
		amt string
	}{cur, amt}
}

func (s *stubMargin) addLoan(clientID string, cur domain.Currency, amt string) {
	s.Lock()
	defer s.Unlock()
	s.loans[clientID] = struct {
		cur domain.Currency
		amt string
	}{cur, amt}
}

var (
	currentSettler = &intStubSettler{}
	currentMargin  = newStubMargin()
)

// setup connects (lazily) to Postgres. Returns a skip reason if the
// stack isn't reachable so tests are skipped rather than failed when
// run outside the dev compose.
func setup(t *testing.T) *Service {
	t.Helper()
	fixOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		dbURL := envOr("INTEGRATION_DATABASE_URL", "postgres://banka:banka@localhost:5432/banka?sslmode=disable")

		pool, err := pgxpool.New(ctx, dbURL)
		if err != nil {
			fixSkip = fmt.Sprintf("postgres connect: %v", err)
			return
		}
		if err := pool.Ping(ctx); err != nil {
			fixSkip = fmt.Sprintf("postgres ping: %v", err)
			return
		}
		// Verify the c3 schema exists. If migrations haven't been
		// applied, skip with a useful message rather than fail.
		var n int
		if err := pool.QueryRow(ctx, `select count(*) from information_schema.tables where table_schema='trading' and table_name='orders'`).Scan(&n); err != nil || n == 0 {
			fixSkip = "trading.orders missing — run migrations first (task migrate)"
			return
		}
		fixPool = pool
	})
	if fixSkip != "" {
		t.Skipf("integration stack unavailable: %s", fixSkip)
	}

	resetSchema(t)

	st := store.New(fixPool)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	belgrade, _ := time.LoadLocation("Europe/Belgrade")
	svc := New(st, Config{
		Belgrade:     belgrade,
		FXCommission: "0.005",
		TickRetry:    100 * time.Millisecond,
	}, logger)
	svc.Rates = pinnedRates{}
	svc.Settler = currentSettler
	svc.TaxSettler = currentSettler
	svc.ForexSettler = currentSettler
	svc.MarginChecker = currentMargin
	currentSettler.reset()
	currentMargin = newStubMargin()
	svc.MarginChecker = currentMargin
	return svc
}

func resetSchema(t *testing.T) {
	t.Helper()
	_, err := fixPool.Exec(context.Background(), `
        truncate
            "trading".realized_gains,
            "trading".portfolio_holdings,
            "trading".order_executions,
            "trading".orders,
            "trading".listing_daily_price_info,
            "trading".listings,
            "trading".securities,
            "trading".exchanges,
            "trading".actuary_info,
            "trading".saga_executions
        restart identity cascade`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// =====================================================================
// Principal helpers
// =====================================================================

func clientCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindClient,
		Permissions: []string{permissions.TradingClient},
	})
}

func clientMarginCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindClient,
		Permissions: []string{permissions.TradingClient, permissions.TradingMargin},
	})
}

func agentCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Actuary, permissions.ActuaryAgent},
	})
}

func supervisorCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Actuary, permissions.ActuarySupervisor, permissions.TradingMargin},
	})
}

// =====================================================================
// Catalog fixtures
// =====================================================================

// seedExchange inserts a USD exchange that is open 24/7 via the
// override flag, so tests don't need to know wall-clock state. NYSE
// MIC is reused for convenience.
func seedExchange(t *testing.T, svc *Service, mic string, currency domain.Currency) *domain.Exchange {
	t.Helper()
	open := true
	e := &domain.Exchange{
		MIC:          mic,
		Name:         mic + " Exchange",
		Acronym:      mic,
		Polity:       "United States",
		Currency:     currency,
		Timezone:     "America/New_York",
		OpenLocal:    "09:30",
		CloseLocal:   "16:00",
		OverrideOpen: &open,
	}
	out, err := svc.Store.UpsertExchange(context.Background(), e)
	if err != nil {
		t.Fatalf("UpsertExchange: %v", err)
	}
	return out
}

// seedStock writes a stock + listing pair. Listing carries an
// ask/bid/price + volume so the cadence + fill-price formulas have
// inputs.
func seedStock(t *testing.T, svc *Service, ticker string, ex *domain.Exchange, price, ask, bid string, volume int64) (*domain.Security, *domain.Listing) {
	t.Helper()
	ctx := context.Background()
	sec, err := svc.Store.UpsertSecurity(ctx, &domain.Security{
		Ticker:            ticker,
		Name:              ticker + " Inc.",
		Type:              domain.SecurityStock,
		ExchangeMIC:       ex.MIC,
		Currency:          ex.Currency,
		OutstandingShares: 1_000_000,
	})
	if err != nil {
		t.Fatalf("UpsertSecurity: %v", err)
	}
	lst, err := svc.Store.UpsertListing(ctx, &domain.Listing{
		SecurityID:   sec.ID,
		ExchangeMIC:  ex.MIC,
		Price:        price,
		Ask:          ask,
		Bid:          bid,
		Volume:       volume,
		ChangeAmt:    "0",
		ContractSize: "1",
	})
	if err != nil {
		t.Fatalf("UpsertListing: %v", err)
	}
	return sec, lst
}

// seedFuture writes a future with a settlement date offset from now.
func seedFuture(t *testing.T, svc *Service, ticker string, ex *domain.Exchange, price string, settles time.Time) (*domain.Security, *domain.Listing) {
	t.Helper()
	ctx := context.Background()
	sec, err := svc.Store.UpsertSecurity(ctx, &domain.Security{
		Ticker:         ticker,
		Name:           ticker + " Future",
		Type:           domain.SecurityFuture,
		ExchangeMIC:    ex.MIC,
		Currency:       ex.Currency,
		ContractSize:   "1000",
		ContractUnit:   "Barrel",
		SettlementDate: &settles,
	})
	if err != nil {
		t.Fatalf("UpsertSecurity future: %v", err)
	}
	lst, err := svc.Store.UpsertListing(ctx, &domain.Listing{
		SecurityID:   sec.ID,
		ExchangeMIC:  ex.MIC,
		Price:        price,
		Ask:          price,
		Bid:          price,
		Volume:       1000,
		ChangeAmt:    "0",
		ContractSize: "1000",
	})
	if err != nil {
		t.Fatalf("UpsertListing future: %v", err)
	}
	return sec, lst
}

// seedActuary stamps an actuary_info row for the given employee. Use
// agentInfo to land a low daily limit so the limit-check tests can
// exercise both the under-limit and over-limit paths.
func seedActuary(t *testing.T, svc *Service, employeeID string, kind domain.ActuaryType, dailyLimitRSD string, needApproval bool) *domain.ActuaryInfo {
	t.Helper()
	out, err := svc.Store.UpsertActuaryInfo(context.Background(), &domain.ActuaryInfo{
		EmployeeID:   employeeID,
		Type:         kind,
		DailyLimit:   dailyLimitRSD,
		NeedApproval: needApproval,
	})
	if err != nil {
		t.Fatalf("UpsertActuaryInfo: %v", err)
	}
	return out
}

// =====================================================================
// Order tests
// =====================================================================

// TestIntegration_CreateOrder_Client_AutoApproved verifies that a
// client's market-buy on a stock auto-approves and stamps approved_by.
func TestIntegration_CreateOrder_Client_AutoApproved(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	clientID := uuid.NewString()
	out, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if out.Status != domain.OrderStatusApproved {
		t.Fatalf("status=%s, want approved", out.Status)
	}
	if out.ApprovedBy != clientID {
		t.Fatalf("approved_by=%s want=%s", out.ApprovedBy, clientID)
	}
	if out.ApprovalRequired {
		t.Fatalf("approval_required should be false for auto-approved")
	}
}

// TestIntegration_CreateOrder_AgentNeedApproval covers spec p.50: an
// agent with need_approval=true always lands pending.
func TestIntegration_CreateOrder_AgentNeedApproval(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	agentID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "100000", true /* needApproval */)

	out, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if out.Status != domain.OrderStatusPending {
		t.Fatalf("status=%s, want pending", out.Status)
	}
	if !out.ApprovalRequired {
		t.Fatalf("approval_required should be true")
	}
}

// TestIntegration_CreateOrder_AgentOverLimit covers spec p.50: a
// trade whose RSD-equivalent pushes used_limit over daily_limit
// routes to pending.
func TestIntegration_CreateOrder_AgentOverLimit(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	agentID := uuid.NewString()
	// Daily limit 1000 RSD — a single AAPL share at $150 ≈ 16,500 RSD
	// so we're well over the cap.
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "1000", false)

	out, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if out.Status != domain.OrderStatusPending {
		t.Fatalf("over-limit agent: status=%s, want pending", out.Status)
	}
}

// TestIntegration_CreateOrder_AgentUnderLimit covers the symmetric
// happy path: trade fits in the cap, auto-approves, used_limit is
// charged.
func TestIntegration_CreateOrder_AgentUnderLimit(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	// Tiny price so 1 share's RSD-equivalent stays under the cap.
	sec, _ := seedStock(t, svc, "PNNY", ex, "1", "1", "1", 1000)

	agentID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "1000000", false)

	out, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if out.Status != domain.OrderStatusApproved {
		t.Fatalf("under-limit agent: status=%s, want approved", out.Status)
	}
	// used_limit should reflect the converted-to-RSD trade value.
	info, err := svc.Store.GetActuaryInfo(context.Background(), agentID)
	if err != nil {
		t.Fatalf("GetActuaryInfo: %v", err)
	}
	if numericEq(info.UsedLimit, "0") || info.UsedLimit == "" {
		t.Fatalf("used_limit should be > 0, got %q", info.UsedLimit)
	}
}

// TestIntegration_CreateOrder_Client_ForexBlocked covers spec p.58:
// clients can't trade forex.
func TestIntegration_CreateOrder_Client_ForexBlocked(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	ctx := context.Background()
	sec, err := svc.Store.UpsertSecurity(ctx, &domain.Security{
		Ticker:        "EURUSD",
		Name:          "Euro / US Dollar",
		Type:          domain.SecurityForex,
		ExchangeMIC:   ex.MIC,
		Currency:      domain.CurrencyUSD,
		BaseCurrency:  domain.CurrencyEUR,
		QuoteCurrency: domain.CurrencyUSD,
		ContractSize:  "1000",
		Liquidity:     "high",
	})
	if err != nil {
		t.Fatalf("UpsertSecurity forex: %v", err)
	}

	_, err = svc.CreateOrder(clientCtx(uuid.NewString()), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("client forex: err=%v, want PermissionDenied", err)
	}
}

// TestIntegration_CreateOrder_SettlementDateGuard covers spec p.50:
// futures whose settlement_date is on/before today are auto-rejected
// at create time.
func TestIntegration_CreateOrder_SettlementDateGuard(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	yesterday := time.Now().Add(-24 * time.Hour)
	sec, _ := seedFuture(t, svc, "CLH22", ex, "70", yesterday)

	_, err := svc.CreateOrder(clientCtx(uuid.NewString()), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("expired future: err=%v, want FailedPrecondition", err)
	}
}

// TestIntegration_Margin_BlockedByBalance covers spec p.55: a margin
// order whose Initial Margin Cost exceeds available balance and the
// client has no loan should be rejected.
func TestIntegration_Margin_BlockedByBalance(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	// Stock priced 100 USD → MM=50, IMC=55 per share. We seed 1 USD on
	// the account so the balance won't cover it.
	sec, _ := seedStock(t, svc, "EXPV", ex, "100", "100", "99", 1000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	currentMargin.addAccount(accID, domain.CurrencyUSD, "1") // ~110 RSD vs ~6055 RSD IMC.

	_, err := svc.CreateOrder(clientMarginCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  accID,
		Margin:     true,
	})
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("margin under-balance: err=%v, want FailedPrecondition", err)
	}
}

// TestIntegration_Margin_PassWithLoan covers the second p.55 limb:
// client has a loan large enough to cover IMC even though account
// balance doesn't.
func TestIntegration_Margin_PassWithLoan(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "EXPV", ex, "100", "100", "99", 1000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	currentMargin.addAccount(accID, domain.CurrencyUSD, "1")
	// IMC ≈ 6055 RSD; 100k RSD loan covers easily.
	currentMargin.addLoan(clientID, domain.CurrencyRSD, "100000")

	out, err := svc.CreateOrder(clientMarginCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  accID,
		Margin:     true,
	})
	if err != nil {
		t.Fatalf("margin with loan: err=%v", err)
	}
	if !out.Margin {
		t.Fatalf("margin flag did not persist")
	}
}

// TestIntegration_Margin_LoanGrantsPermission verifies that a client
// without TradingMargin still passes the permission gate when they
// hold an approved loan (spec p.55 "automatski dobija ovu permisiju").
func TestIntegration_Margin_LoanGrantsPermission(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "EXPV", ex, "100", "100", "99", 1000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	currentMargin.addAccount(accID, domain.CurrencyUSD, "10000") // covers IMC outright
	currentMargin.addLoan(clientID, domain.CurrencyRSD, "100000")

	// clientCtx — no TradingMargin permission.
	out, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  accID,
		Margin:     true,
	})
	if err != nil {
		t.Fatalf("loan-derived margin permission: err=%v", err)
	}
	if !out.Margin {
		t.Fatalf("margin flag did not persist")
	}
}

// TestIntegration_Margin_ClientNoLoanNoPermission asserts that a
// client without TradingMargin and without an approved loan is denied.
func TestIntegration_Margin_ClientNoLoanNoPermission(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "EXPV", ex, "100", "100", "99", 1000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	currentMargin.addAccount(accID, domain.CurrencyUSD, "10000")
	// no addLoan — client has no loan.

	_, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  accID,
		Margin:     true,
	})
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("no-loan no-perm margin: err=%v, want PermissionDenied", err)
	}
}

// TestIntegration_ApproveRechecksSettlement covers fix #9 / spec p.50:
// a pending order whose security passes settlement date between create
// and approve auto-declines on approve.
func TestIntegration_ApproveRechecksSettlement(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	tomorrow := time.Now().Add(24 * time.Hour)
	sec, _ := seedFuture(t, svc, "CLM26", ex, "70", tomorrow)

	supervisorID := uuid.NewString()
	agentID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "1000000", true /* needApproval */)

	pending, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}

	// Move the security's settlement_date into the past behind the
	// service's back; the next approval must auto-decline.
	yesterday := time.Now().Add(-24 * time.Hour)
	if _, err := fixPool.Exec(context.Background(),
		`update "trading".securities set settlement_date=$1 where id=$2`, yesterday, sec.ID); err != nil {
		t.Fatalf("update settlement: %v", err)
	}

	declined, err := svc.ApproveOrder(supervisorCtx(supervisorID), pending.ID)
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("expired-on-approve: err=%v, want FailedPrecondition", err)
	}
	if declined == nil || declined.Status != domain.OrderStatusDeclined {
		t.Fatalf("auto-declined order should have status=declined, got %+v", declined)
	}
}

// TestIntegration_ApproveDeclineCancel walks the supervisor lifecycle.
func TestIntegration_ApproveDeclineCancel(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	supervisorID := uuid.NewString()
	agentID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "100", true)

	// 1. Submit, expect pending.
	pending, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}

	// 2. Approve.
	approved, err := svc.ApproveOrder(supervisorCtx(supervisorID), pending.ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != domain.OrderStatusApproved {
		t.Fatalf("after approve, status=%s", approved.Status)
	}

	// 3. Re-approval is a no-op error.
	if _, err := svc.ApproveOrder(supervisorCtx(supervisorID), pending.ID); !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("double approve: err=%v, want FailedPrecondition", err)
	}

	// 4. Cancel by supervisor lands cancelled.
	cancelled, err := svc.CancelOrder(supervisorCtx(supervisorID), pending.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !cancelled.Cancelled {
		t.Fatalf("after cancel, cancelled flag should be true")
	}

	// 5. Decline a fresh pending.
	pending2, _ := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	declined, err := svc.DeclineOrder(supervisorCtx(supervisorID), pending2.ID, "test")
	if err != nil {
		t.Fatalf("decline: %v", err)
	}
	if declined.Status != domain.OrderStatusDeclined {
		t.Fatalf("after decline, status=%s", declined.Status)
	}
}

// =====================================================================
// Execution tests
// =====================================================================

// TestIntegration_Execution_StopTriggersOnAsk verifies fix #1 — a buy-
// stop fires on ask>stop, not last-price.
func TestIntegration_Execution_StopTriggersOnAsk(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)

	// last_price=100 sits at the stop, but ask=99 is below — so stop
	// should NOT trigger. A naive last-vs-stop check would (incorrectly)
	// trigger here.
	sec, _ := seedStock(t, svc, "STOPA", ex, "100", "99", "98", 100000)

	clientID := uuid.NewString()
	o, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderStop,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		StopPrice:  "100",
		AllOrNone:  true, // skip random cadence so trigger+fire happen on the same tick
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create stop: %v", err)
	}
	res, err := svc.ProcessOrderTick(context.Background(), o)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.Fired {
		t.Fatalf("stop should NOT trigger when ask<=stop")
	}

	// Bump the ask above stop; the next tick should trigger and fire.
	if _, err := fixPool.Exec(context.Background(),
		`update "trading".listings set ask='101' where security_id=$1`, sec.ID); err != nil {
		t.Fatalf("update ask: %v", err)
	}
	o2, _ := svc.Store.GetOrder(context.Background(), o.ID)
	res, err = svc.ProcessOrderTick(context.Background(), o2)
	if err != nil {
		t.Fatalf("tick2: %v", err)
	}
	if !res.Fired {
		t.Fatalf("stop should trigger when ask>stop, got fired=false")
	}
}

// TestIntegration_Execution_LimitFillPriceMin verifies fix #2 —
// a buy-limit fills at min(limit, ask).
func TestIntegration_Execution_LimitFillPriceMin(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	// limit=100, ask=95 → fill at 95.
	sec, _ := seedStock(t, svc, "LIMA", ex, "95", "95", "94", 100000)

	clientID := uuid.NewString()
	o, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderLimit,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		LimitPrice: "100",
		AllOrNone:  true, // single fill, no random sub-quantity
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create limit: %v", err)
	}
	res, err := svc.ProcessOrderTick(context.Background(), o)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !res.Fired || res.Execution == nil {
		t.Fatalf("expected fill, got fired=%v exec=%v", res.Fired, res.Execution)
	}
	// Compare numerically — store renders to 4 decimal places.
	if !numericEq(res.Execution.PricePerUnit, "95") {
		t.Fatalf("fill price = %s, want 95 (min(limit=100, ask=95))", res.Execution.PricePerUnit)
	}
}

// TestIntegration_Execution_AONFullFill verifies AON fills the whole
// order in one shot.
func TestIntegration_Execution_AONFullFill(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AONX", ex, "10", "10", "9", 1000000)

	clientID := uuid.NewString()
	o, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   5,
		AllOrNone:  true,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create AON: %v", err)
	}
	res, err := svc.ProcessOrderTick(context.Background(), o)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !res.Fired || res.Execution == nil {
		t.Fatalf("AON did not fire on first tick")
	}
	if res.Execution.Quantity != 5 {
		t.Fatalf("AON fill qty=%d, want 5", res.Execution.Quantity)
	}
	// Holding should reflect the full fill.
	post, _ := svc.Store.GetOrder(context.Background(), o.ID)
	if !post.IsDone {
		t.Fatalf("AON: is_done should be true after one fill")
	}
}

// TestIntegration_Execution_BuyHoldingAndSellRealizesGain walks a
// buy → sell sequence and asserts the holding + realized_gain rows.
func TestIntegration_Execution_BuyHoldingAndSellRealizesGain(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "GAIN", ex, "100", "100", "99", 1000000)

	clientID := uuid.NewString()
	accID := uuid.NewString()

	// Buy 2 shares AON.
	buy, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   2,
		AllOrNone:  true,
		AccountID:  accID,
	})
	if err != nil {
		t.Fatalf("create buy: %v", err)
	}
	if _, err := svc.ProcessOrderTick(context.Background(), buy); err != nil {
		t.Fatalf("buy tick: %v", err)
	}

	// Bump the price and sell.
	if _, err := fixPool.Exec(context.Background(),
		`update "trading".listings set price='150', ask='150', bid='149' where security_id=$1`, sec.ID); err != nil {
		t.Fatalf("bump price: %v", err)
	}

	sell, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionSell,
		Quantity:   2,
		AllOrNone:  true,
		AccountID:  accID,
	})
	if err != nil {
		t.Fatalf("create sell: %v", err)
	}
	if _, err := svc.ProcessOrderTick(context.Background(), sell); err != nil {
		t.Fatalf("sell tick: %v", err)
	}

	// Holding must be drained.
	holdings, err := svc.Store.ListHoldings(context.Background(), store.HoldingFilter{UserID: clientID})
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	for _, h := range holdings {
		if h.SecurityID == sec.ID && h.Quantity != 0 {
			t.Fatalf("holding qty after sell = %d, want 0", h.Quantity)
		}
	}

	// Realized-gain row exists with positive RSD.
	gains, err := svc.Store.ListRealizedGains(context.Background(), store.RealizedGainFilter{UserID: clientID})
	if err != nil {
		t.Fatalf("ListRealizedGains: %v", err)
	}
	if len(gains) == 0 {
		t.Fatalf("expected at least one realized-gain row")
	}
	// Buy fills at ask=100, sell-market takes bid=149.
	// gain_native = (149-100)*2 = 98.
	if !numericEq(gains[0].GainNative, "98") {
		t.Fatalf("gain_native = %s, want 98", gains[0].GainNative)
	}
}

// TestIntegration_Execution_ForexNoHolding verifies fix #8: a forex
// order does NOT create a portfolio_holding row even though it goes
// through executeFill.
func TestIntegration_Execution_ForexNoHolding(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	ctx := context.Background()
	sec, err := svc.Store.UpsertSecurity(ctx, &domain.Security{
		Ticker:        "EURUSD",
		Name:          "Euro / US Dollar",
		Type:          domain.SecurityForex,
		ExchangeMIC:   ex.MIC,
		Currency:      domain.CurrencyUSD,
		BaseCurrency:  domain.CurrencyEUR,
		QuoteCurrency: domain.CurrencyUSD,
		ContractSize:  "1000",
		Liquidity:     "high",
	})
	if err != nil {
		t.Fatalf("UpsertSecurity forex: %v", err)
	}
	if _, err := svc.Store.UpsertListing(ctx, &domain.Listing{
		SecurityID: sec.ID, ExchangeMIC: ex.MIC,
		Price: "1.10", Ask: "1.10", Bid: "1.09",
		Volume: 1000000, ChangeAmt: "0", ContractSize: "1000",
	}); err != nil {
		t.Fatalf("UpsertListing forex: %v", err)
	}

	agentID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "10000000", false)

	o, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AllOrNone:  true,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create forex order: %v", err)
	}
	res, err := svc.ProcessOrderTick(context.Background(), o)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !res.Fired {
		t.Fatalf("expected forex fill to fire")
	}
	holdings, _ := svc.Store.ListHoldings(context.Background(), store.HoldingFilter{UserID: agentID})
	for _, h := range holdings {
		if h.SecurityID == sec.ID {
			t.Fatalf("forex created a holding row; spec p.42 forbids that. holding=%+v", h)
		}
	}

	// Paired forex settlement should have hit the ForexSettler exactly
	// once per fill, never the regular Settler.
	if len(currentSettler.forexCalls) != 1 {
		t.Fatalf("expected 1 forex settle call, got %d", len(currentSettler.forexCalls))
	}
	fx := currentSettler.forexCalls[0]
	if fx.Direction != "buy" || fx.BaseCurrency != domain.CurrencyEUR || fx.QuoteCurrency != domain.CurrencyUSD {
		t.Fatalf("forex call mis-shaped: %+v", fx)
	}
	if !numericEq(fx.BaseAmount, "1000") {
		t.Fatalf("forex base_amount = %s, want 1000 (qty=1 × cs=1000)", fx.BaseAmount)
	}
	if !numericEq(fx.QuoteAmount, "1100") {
		t.Fatalf("forex quote_amount = %s, want 1100 (1000 × 1.10)", fx.QuoteAmount)
	}
	if len(currentSettler.settleCalls) != 0 {
		t.Fatalf("forex orders must not hit the regular SettleTrade path; got %d calls",
			len(currentSettler.settleCalls))
	}
}

// =====================================================================
// Tax tests
// =====================================================================

// TestIntegration_Tax_RunCharges15Pct seeds a realized_gain row and
// asserts RunTax dispatches a SettleTax of 15% × gain_rsd.
func TestIntegration_Tax_RunCharges15Pct(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "TAXED", ex, "100", "100", "99", 1000)

	clientID := uuid.NewString()
	accID := uuid.NewString()

	// Hand-write a realized-gain row of 1000 RSD profit.
	if err := writeRealizedGain(t, svc, clientID, sec.ID, accID, "1000"); err != nil {
		t.Fatalf("writeRealizedGain: %v", err)
	}

	res, err := svc.RunTax(TaxCronContext(context.Background()), RunTaxInput{})
	if err != nil {
		t.Fatalf("RunTax: %v", err)
	}
	if res.UsersTaxed != 1 {
		t.Fatalf("users_taxed=%d, want 1", res.UsersTaxed)
	}
	// 15% × 1000 = 150
	if !numericEq(res.TotalCollectedRSD, "150") {
		t.Fatalf("collected=%s, want 150", res.TotalCollectedRSD)
	}
	calls := currentSettler.taxCalls
	if len(calls) != 1 {
		t.Fatalf("expected 1 tax settle call, got %d", len(calls))
	}
	if calls[0].AccountID != accID {
		t.Fatalf("tax debited wrong account: %s vs %s", calls[0].AccountID, accID)
	}

	// Second run is a no-op — rows are taxed=true.
	res2, err := svc.RunTax(TaxCronContext(context.Background()), RunTaxInput{})
	if err != nil {
		t.Fatalf("RunTax 2: %v", err)
	}
	if res2.UsersTaxed != 0 {
		t.Fatalf("re-run: users_taxed=%d, want 0", res2.UsersTaxed)
	}
}

// TestIntegration_Tax_LossClamped covers the loss-only path: a
// negative gain_rsd row is consumed (taxed=true) but contributes zero
// to the bank-side debit.
func TestIntegration_Tax_LossClamped(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "LOSS", ex, "100", "100", "99", 1000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	if err := writeRealizedGain(t, svc, clientID, sec.ID, accID, "-500"); err != nil {
		t.Fatalf("writeRealizedGain: %v", err)
	}

	res, err := svc.RunTax(TaxCronContext(context.Background()), RunTaxInput{})
	if err != nil {
		t.Fatalf("RunTax: %v", err)
	}
	if res.UsersTaxed != 0 {
		t.Fatalf("loss-only: users_taxed=%d, want 0", res.UsersTaxed)
	}
	if len(currentSettler.taxCalls) != 0 {
		t.Fatalf("expected no SettleTax calls for loss-only run")
	}
}

// stubUsers is a deterministic UserResolver. The integration suite
// wires it into the service before exercising the supervisor tax board
// so display_name + name_query are exercised without spinning up the
// user service.
type stubUsers struct {
	names map[string]string // user_id → display name
}

func (s *stubUsers) DisplayName(_ context.Context, userID string, _ domain.UserKind) (string, error) {
	if n, ok := s.names[userID]; ok {
		return n, nil
	}
	return "", nil
}

// writeRealizedGainAt seeds a realized_gain row at a chosen wall-clock
// time so date-range filtering can be exercised. realized_at defaults
// to now() when omitted; the integration tests below pin it explicitly.
func writeRealizedGainAt(t *testing.T, userID, secID, accID, gainRSD string, realizedAt time.Time) error {
	t.Helper()
	const q = `
        insert into "trading".realized_gains
            (user_id, user_kind, security_id, account_id, quantity,
             cost_basis_amt, proceeds_amt, currency,
             gain_native, gain_rsd, realized_at)
        values ($1,'client',$2,$3,1,
                100::numeric, 200::numeric, 'RSD',
                $4::numeric, $4::numeric, $5)`
	_, err := fixPool.Exec(context.Background(), q, userID, secID, accID, gainRSD, realizedAt)
	return err
}

// TestIntegration_ListRealizedPnL covers the supervisor detail-view
// RPC: user filter, date-range clipping, ticker decoration, and the
// per-row tax_amount math (15% of profit_rsd, clamped to 0 for losses).
func TestIntegration_ListRealizedPnL(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "PNL", ex, "100", "100", "99", 1000)

	clientID := uuid.NewString()
	otherID := uuid.NewString()
	accID := uuid.NewString()

	// Three rows for clientID at distinct dates; one row for otherID
	// to verify the user filter.
	jan := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	feb := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	if err := writeRealizedGainAt(t, clientID, sec.ID, accID, "1000", jan); err != nil {
		t.Fatalf("seed jan: %v", err)
	}
	if err := writeRealizedGainAt(t, clientID, sec.ID, accID, "-500", feb); err != nil {
		t.Fatalf("seed feb (loss): %v", err)
	}
	if err := writeRealizedGainAt(t, clientID, sec.ID, accID, "2000", mar); err != nil {
		t.Fatalf("seed mar: %v", err)
	}
	if err := writeRealizedGainAt(t, otherID, sec.ID, accID, "9999", feb); err != nil {
		t.Fatalf("seed other: %v", err)
	}

	ctx := TaxCronContext(context.Background())
	rows, err := svc.ListRealizedPnL(ctx, ListRealizedPnLInput{UserID: clientID})
	if err != nil {
		t.Fatalf("ListRealizedPnL all: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows=%d, want 3 (only clientID)", len(rows))
	}
	for _, r := range rows {
		if r.Ticker != "PNL" {
			t.Fatalf("ticker=%q, want PNL", r.Ticker)
		}
	}
	// Loss row is preserved with tax=0; gain rows compute 15%.
	for _, r := range rows {
		var want string
		switch {
		case numericEq(r.ProfitRSD, "1000"):
			want = "150"
		case numericEq(r.ProfitRSD, "2000"):
			want = "300"
		case numericEq(r.ProfitRSD, "-500"):
			want = "0"
		default:
			t.Fatalf("unexpected profit_rsd=%q", r.ProfitRSD)
		}
		if !numericEq(r.TaxAmountRSD, want) {
			t.Fatalf("profit=%q → tax=%q, want %q", r.ProfitRSD, r.TaxAmountRSD, want)
		}
	}

	// Date filter: only Feb-Mar.
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC)
	clipped, err := svc.ListRealizedPnL(ctx, ListRealizedPnLInput{
		UserID: clientID, From: &from, To: &to,
	})
	if err != nil {
		t.Fatalf("ListRealizedPnL clipped: %v", err)
	}
	if len(clipped) != 2 {
		t.Fatalf("clipped rows=%d, want 2 (Feb+Mar)", len(clipped))
	}
}

// TestIntegration_ListTaxPositions_DisplayName covers the spec p.63
// "filteri po imenu i prezimenu" path: rows carry the resolved name,
// and name_query filters case-insensitively against it.
func TestIntegration_ListTaxPositions_DisplayName(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "DNQ", ex, "100", "100", "99", 1000)

	pera := uuid.NewString()
	mika := uuid.NewString()
	accID := uuid.NewString()
	now := time.Now()
	if err := writeRealizedGainAt(t, pera, sec.ID, accID, "1000", now); err != nil {
		t.Fatalf("seed pera: %v", err)
	}
	if err := writeRealizedGainAt(t, mika, sec.ID, accID, "2000", now); err != nil {
		t.Fatalf("seed mika: %v", err)
	}

	svc.Users = &stubUsers{names: map[string]string{
		pera: "Petar Petrović",
		mika: "Mika Mikić",
	}}

	ctx := TaxCronContext(context.Background())
	all, err := svc.ListTaxPositions(ctx, ListTaxPositionsInput{})
	if err != nil {
		t.Fatalf("ListTaxPositions: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("rows=%d, want 2", len(all))
	}
	gotName := map[string]string{}
	for _, r := range all {
		gotName[r.UserID] = r.DisplayName
	}
	if gotName[pera] != "Petar Petrović" {
		t.Fatalf("pera display_name=%q", gotName[pera])
	}

	// Name filter: case-insensitive substring against display_name.
	filtered, err := svc.ListTaxPositions(ctx, ListTaxPositionsInput{NameQuery: "petr"})
	if err != nil {
		t.Fatalf("ListTaxPositions filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].UserID != pera {
		t.Fatalf("filter petr → %#v, want only pera", filtered)
	}
}

func writeRealizedGain(t *testing.T, svc *Service, userID, secID, accID, gainRSD string) error {
	t.Helper()
	tx, err := fixPool.Begin(context.Background())
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err := svc.Store.InsertRealizedGain(context.Background(), tx, &domain.RealizedGain{
		UserID:       userID,
		UserKind:     domain.KindClient,
		SecurityID:   secID,
		AccountID:    accID,
		Quantity:     1,
		CostBasisAmt: "100",
		ProceedsAmt:  "200",
		Currency:     domain.CurrencyRSD,
		GainNative:   gainRSD,
		GainRSD:      gainRSD,
	}); err != nil {
		return err
	}
	return tx.Commit(context.Background())
}
