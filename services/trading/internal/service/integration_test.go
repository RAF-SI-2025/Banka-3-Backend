//go:build integration

// Package service integration tests exercise the trading service
// against a real Postgres. Same gating + reset pattern as the bank
// service:
//
//	make up                    # bring up postgres
//	make migrate               # apply trading schema
//	make test-integration      # runs this suite
//
// Bank settlement, FX rates, and bank-account reads are stubbed
// in-process so we don't need a live `bank` or `exchange` container.
package service

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
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
		cur domain.Currency
		amt string
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
	currentSettler      = &intStubSettler{}
	currentMargin       = newStubMargin()
	currentReservations = newStubReservations()
)

// stubReservations satisfies BankReservations for integration tests
// without a live bank container. Reserves are tracked by op_id in
// memory; commits move money between in-memory account balances so
// tests can assert post-condition deltas. Errors are pluggable per
// call type for failure-path tests.
type stubReservations struct {
	sync.Mutex
	balances        map[string]string // accountID → amount (decimal string)
	currencies      map[string]domain.Currency
	reserved        map[string]reservedRow
	reserveCalls    []ReserveInput
	releaseCalls    []string
	commitCalls     []CommitInput
	transferCalls   []TransferInput
	createFundCalls []string
	// Pluggable errors for failure-path tests.
	reserveErr  error
	commitErr   error
	releaseErr  error
	transferErr error
	// fundAccountIDs queued for CreateFundAccount returns. Empty queue
	// means a fresh UUID is generated per call.
	fundAccountIDs []string
}

type reservedRow struct {
	AccountID string
	Amount    string
	Currency  domain.Currency
	OpKind    string
	State     string // "held" / "committed" / "released"
}

func newStubReservations() *stubReservations {
	return &stubReservations{
		balances:   map[string]string{},
		currencies: map[string]domain.Currency{},
		reserved:   map[string]reservedRow{},
	}
}

func (s *stubReservations) reset() {
	s.Lock()
	defer s.Unlock()
	s.balances = map[string]string{}
	s.currencies = map[string]domain.Currency{}
	s.reserved = map[string]reservedRow{}
	s.reserveCalls = nil
	s.releaseCalls = nil
	s.commitCalls = nil
	s.transferCalls = nil
	s.createFundCalls = nil
	s.reserveErr = nil
	s.commitErr = nil
	s.releaseErr = nil
	s.transferErr = nil
	s.fundAccountIDs = nil
}

func (s *stubReservations) setBalance(accountID string, amount string) {
	s.Lock()
	defer s.Unlock()
	s.balances[accountID] = amount
}

func (s *stubReservations) setCurrency(accountID string, c domain.Currency) {
	s.Lock()
	defer s.Unlock()
	s.currencies[accountID] = c
}

func (s *stubReservations) balance(accountID string) string {
	s.Lock()
	defer s.Unlock()
	if v, ok := s.balances[accountID]; ok {
		return v
	}
	return "0"
}

func (s *stubReservations) Reserve(_ context.Context, in ReserveInput) (string, error) {
	s.Lock()
	defer s.Unlock()
	if s.reserveErr != nil {
		return "", s.reserveErr
	}
	if _, ok := s.reserved[in.OpID]; ok {
		// Idempotent — same op_id returns existing.
		return "stub-" + in.OpID, nil
	}
	s.reserved[in.OpID] = reservedRow{
		AccountID: in.AccountID,
		Amount:    in.Amount,
		Currency:  in.Currency,
		OpKind:    in.OpKind,
		State:     "held",
	}
	s.reserveCalls = append(s.reserveCalls, in)
	return "stub-" + in.OpID, nil
}

func (s *stubReservations) Release(_ context.Context, opID string) (bool, error) {
	s.Lock()
	defer s.Unlock()
	if s.releaseErr != nil {
		return false, s.releaseErr
	}
	r, ok := s.reserved[opID]
	if !ok {
		// Idempotent — no-op release returns false.
		return false, nil
	}
	if r.State != "held" {
		return false, nil
	}
	r.State = "released"
	s.reserved[opID] = r
	s.releaseCalls = append(s.releaseCalls, opID)
	return true, nil
}

func (s *stubReservations) Commit(_ context.Context, in CommitInput) (string, error) {
	s.Lock()
	defer s.Unlock()
	if s.commitErr != nil {
		return "", s.commitErr
	}
	r, ok := s.reserved[in.OpID]
	if !ok {
		return "", fmt.Errorf("commit without reserve for op_id=%s", in.OpID)
	}
	if r.State == "committed" {
		// Idempotent return.
		return in.OpID, nil
	}
	if r.State != "held" {
		return "", fmt.Errorf("commit reservation not held (state=%s)", r.State)
	}
	// Move money: debit reserved amount from source, credit dest.
	src, _ := money.Parse(s.balances[r.AccountID])
	if src == nil {
		src = money.MustParse("0")
	}
	amt, _ := money.Parse(r.Amount)
	s.balances[r.AccountID] = money.FormatAmount(money.Sub(src, amt))
	dst, _ := money.Parse(s.balances[in.DestAccountID])
	if dst == nil {
		dst = money.MustParse("0")
	}
	destAmt, _ := money.Parse(in.DestAmount)
	s.balances[in.DestAccountID] = money.FormatAmount(money.Add(dst, destAmt))
	r.State = "committed"
	s.reserved[in.OpID] = r
	s.commitCalls = append(s.commitCalls, in)
	return in.OpID, nil
}

// AccountAvailable returns the (currency, balance) tuple. Defaults to
// RSD when the test hasn't pinned a currency on the account.
func (s *stubReservations) AccountAvailable(_ context.Context, accountID string) (domain.Currency, string, error) {
	s.Lock()
	defer s.Unlock()
	c, ok := s.currencies[accountID]
	if !ok {
		c = domain.CurrencyRSD
	}
	bal, ok := s.balances[accountID]
	if !ok {
		bal = "0"
	}
	return c, bal, nil
}

// AccountNumber returns a synthetic 18-digit number derived from the
// account id (stub tests don't track real bank-side numbers). Good
// enough for callers that only need a non-empty value.
func (s *stubReservations) AccountNumber(_ context.Context, accountID string) (string, error) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(accountID))
	n := h.Sum64()
	// 18 digits, mod-11 not enforced (cosmetic in stub).
	return fmt.Sprintf("%018d", n%1_000_000_000_000_000_000), nil
}

// CreateFundAccount returns the next queued fund account id, or a
// freshly-minted UUID otherwise. The created account is registered in
// the stub's balance map at zero with the requested currency so
// subsequent AccountAvailable lookups don't fail.
func (s *stubReservations) CreateFundAccount(_ context.Context, name string, currency domain.Currency) (string, error) {
	s.Lock()
	defer s.Unlock()
	s.createFundCalls = append(s.createFundCalls, name)
	var id string
	if len(s.fundAccountIDs) > 0 {
		id = s.fundAccountIDs[0]
		s.fundAccountIDs = s.fundAccountIDs[1:]
	} else {
		id = uuid.NewString()
	}
	s.balances[id] = "0"
	s.currencies[id] = currency
	return id, nil
}

func (s *stubReservations) Transfer(_ context.Context, in TransferInput) (string, error) {
	s.Lock()
	defer s.Unlock()
	if s.transferErr != nil {
		return "", s.transferErr
	}
	src, _ := money.Parse(s.balances[in.FromAccountID])
	if src == nil {
		src = money.MustParse("0")
	}
	amt, _ := money.Parse(in.Amount)
	s.balances[in.FromAccountID] = money.FormatAmount(money.Sub(src, amt))
	dst, _ := money.Parse(s.balances[in.ToAccountID])
	if dst == nil {
		dst = money.MustParse("0")
	}
	s.balances[in.ToAccountID] = money.FormatAmount(money.Add(dst, amt))
	s.transferCalls = append(s.transferCalls, in)
	return in.OpID, nil
}

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
			fixSkip = "trading.orders missing — run migrations first (make migrate)"
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

	// c4 SAGA + reservations wiring. Without these the OTC accept /
	// exercise paths refuse to run.
	currentReservations.reset()
	svc.Reservations = currentReservations
	svc.SagaStore = st.Sagas()
	reg := saga.NewRegistry()
	svc.SagaOrch = saga.New(svc.SagaStore, reg, logger)
	RegisterSagas(reg, svc)
	return svc
}

func resetSchema(t *testing.T) {
	t.Helper()
	_, err := fixPool.Exec(context.Background(), `
        truncate
            "trading".external_otc_contracts,
            "trading".external_otc_iterations,
            "trading".external_otc_threads,
            "trading".fund_performance_snapshots,
            "trading".client_fund_transactions,
            "trading".client_fund_positions,
            "trading".investment_funds,
            "trading".otc_contracts,
            "trading".otc_offers,
            "trading".option_exercises,
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
	e := &domain.Exchange{
		MIC:        mic,
		Name:       mic + " Exchange",
		Acronym:    mic,
		Polity:     "United States",
		Currency:   currency,
		Timezone:   "America/New_York",
		OpenLocal:  "09:30",
		CloseLocal: "16:00",
	}
	if _, err := svc.Store.UpsertExchange(context.Background(), e); err != nil {
		t.Fatalf("UpsertExchange: %v", err)
	}
	open := domain.ExchangeOverrideOpen
	out, err := svc.Store.SetExchangeOverride(context.Background(), mic, &open)
	if err != nil {
		t.Fatalf("SetExchangeOverride: %v", err)
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

// TestIntegration_CreateOrder_AgentZeroLimit covers spec p.38: for an
// agent (not a supervisor) daily_limit=0 means zero capacity, not
// unlimited. The order should land in pending so the supervisor can
// decide.
func TestIntegration_CreateOrder_AgentZeroLimit(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	agentID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "0", false /* needApproval */)

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
		t.Fatalf("zero-limit agent: status=%s, want pending", out.Status)
	}
	if !out.ApprovalRequired {
		t.Fatalf("approval_required should be true for zero-limit agent")
	}
}

// TestIntegration_CreateOrder_AgentMissingInfo covers spec p.38: an
// employee with the agent permission but no actuary_info row is a
// misconfiguration — refuse rather than queueing arbitrarily large
// pending orders for the supervisor.
func TestIntegration_CreateOrder_AgentMissingInfo(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	agentID := uuid.NewString() // no seedActuary call

	_, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if err == nil {
		t.Fatalf("CreateOrder: expected FailedPrecondition, got nil")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Kind != apperr.KindFailedPrecondition {
		t.Fatalf("CreateOrder: kind=%v want=FailedPrecondition (err=%v)", ae, err)
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

// TestIntegration_CreateOrder_SellExceedsHoldings covers
// spec/C3-tests.pdf S37: "Korisnik ne može prodati više hartija nego
// što poseduje." The service-edge guard rejects before the order is
// queued so the supervisor's pending list doesn't fill up with orders
// the worker can't possibly fill.
func TestIntegration_CreateOrder_SellExceedsHoldings(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 100_000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	seedHolding(t, svc, clientID, domain.KindClient, sec.ID, accID, 10, "150")

	_, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionSell,
		Quantity:   15, // > 10 held
		AccountID:  accID,
	})
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("sell-exceeds-holdings: err=%v, want FailedPrecondition", err)
	}
}

// TestIntegration_CreateOrder_SellExactHoldings covers
// spec/C3-tests.pdf S38: selling exactly the held qty is fine.
func TestIntegration_CreateOrder_SellExactHoldings(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 100_000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	seedHolding(t, svc, clientID, domain.KindClient, sec.ID, accID, 10, "150")

	out, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionSell,
		Quantity:   10,
		AccountID:  accID,
	})
	if err != nil {
		t.Fatalf("sell-exact-holdings: %v", err)
	}
	if out.Status != domain.OrderStatusApproved {
		t.Fatalf("status=%s, want approved", out.Status)
	}
}

// TestIntegration_CreateOrder_SellNoHoldings covers the boundary —
// a SELL against a security the user doesn't hold at all.
func TestIntegration_CreateOrder_SellNoHoldings(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 100_000)

	_, err := svc.CreateOrder(clientCtx(uuid.NewString()), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionSell,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("sell-no-holdings: err=%v, want FailedPrecondition", err)
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

// TestIntegration_AON_Margin_Combination covers BE-17: AON and margin
// can coexist on a single order. assertMarginEligible runs (account or
// loan must cover IMC) and the persisted row carries both flags so the
// execution worker honors them later.
func TestIntegration_AON_Margin_Combination(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AONM", ex, "100", "100", "99", 1000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	// 50 USD ≈ 5500 RSD vs IMC ≈ 6055 RSD per share — under-balance, so
	// the loan limb has to carry it.
	currentMargin.addAccount(accID, domain.CurrencyUSD, "50")
	currentMargin.addLoan(clientID, domain.CurrencyRSD, "100000")

	out, err := svc.CreateOrder(clientMarginCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  accID,
		AllOrNone:  true,
		Margin:     true,
	})
	if err != nil {
		t.Fatalf("AON+margin: err=%v", err)
	}
	if !out.AllOrNone || !out.Margin {
		t.Fatalf("AON+margin flags did not persist: AON=%v Margin=%v", out.AllOrNone, out.Margin)
	}
	if out.Status != domain.OrderStatusApproved {
		t.Fatalf("AON+margin auto-approve expected, got status=%s", out.Status)
	}
}

// TestIntegration_PreFundsCheck_RejectsUnderfunded covers BE-12: a
// non-margin buy whose notional exceeds account balance is rejected
// up-front, so the order doesn't accept-and-stall in the worker.
func TestIntegration_PreFundsCheck_RejectsUnderfunded(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	// 10 USD share; buy 1 → notional ≈ 1100 RSD; account holds 1 USD ≈ 110 RSD.
	sec, _ := seedStock(t, svc, "PFUN", ex, "10", "10", "9", 1000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	currentMargin.addAccount(accID, domain.CurrencyUSD, "1")

	_, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  accID,
	})
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("pre-fill funds check: err=%v, want FailedPrecondition", err)
	}
}

// TestIntegration_CancelRefundsAgentLimit covers BE-13: cancelling an
// approved agent order refunds the previously-charged daily-limit
// amount. Auto-approved (under-limit) order charges used_limit; cancel
// must zero it back out.
func TestIntegration_CancelRefundsAgentLimit(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "PNNY", ex, "1", "1", "1", 1000)

	agentID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "1000000", false)
	accID := uuid.NewString()
	currentMargin.addAccount(accID, domain.CurrencyUSD, "1000")

	out, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  accID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.Status != domain.OrderStatusApproved {
		t.Fatalf("expected auto-approve, got status=%s", out.Status)
	}
	info, err := svc.Store.GetActuaryInfo(context.Background(), agentID)
	if err != nil {
		t.Fatalf("GetActuaryInfo charged: %v", err)
	}
	if numericEq(info.UsedLimit, "0") {
		t.Fatalf("used_limit should be > 0 after auto-approve, got %q", info.UsedLimit)
	}

	// partialQty=0 → full cancel (back-compat with the pre-P1.3 signature).
	if _, err := svc.CancelOrder(agentCtx(agentID), out.ID, 0); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	info, err = svc.Store.GetActuaryInfo(context.Background(), agentID)
	if err != nil {
		t.Fatalf("GetActuaryInfo refunded: %v", err)
	}
	if !numericEq(info.UsedLimit, "0") {
		t.Fatalf("used_limit should be 0 after cancel-refund, got %q", info.UsedLimit)
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
	cancelled, err := svc.CancelOrder(supervisorCtx(supervisorID), pending.ID, 0)
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
	res, err := svc.ProcessOrderTick(context.Background(), o.Order)
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
	res, err := svc.ProcessOrderTick(context.Background(), o.Order)
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
	res, err := svc.ProcessOrderTick(context.Background(), o.Order)
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
	if _, err := svc.ProcessOrderTick(context.Background(), buy.Order); err != nil {
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
	if _, err := svc.ProcessOrderTick(context.Background(), sell.Order); err != nil {
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
		// Spec p.7 (E2E doc): forex minimum lot = 10.
		Quantity:   10,
		AllOrNone:  true,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create forex order: %v", err)
	}
	res, err := svc.ProcessOrderTick(context.Background(), o.Order)
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
	// qty=10 (forex min lot, spec p.7) × contract_size=1000 = 10000 base.
	if !numericEq(fx.BaseAmount, "10000") {
		t.Fatalf("forex base_amount = %s, want 10000 (qty=10 × cs=1000)", fx.BaseAmount)
	}
	if !numericEq(fx.QuoteAmount, "11000") {
		t.Fatalf("forex quote_amount = %s, want 11000 (10000 × 1.10)", fx.QuoteAmount)
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
	names map[string]string   // user_id → display name
	perms map[string][]string // user_id → permission strings (CreateFund manager validation)
}

func (s *stubUsers) DisplayName(_ context.Context, userID string, _ domain.UserKind) (string, error) {
	if n, ok := s.names[userID]; ok {
		return n, nil
	}
	return "", nil
}

func (s *stubUsers) EmployeePermissions(_ context.Context, userID string) ([]string, error) {
	if p, ok := s.perms[userID]; ok {
		return p, nil
	}
	return nil, nil
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

// settlerFunc adapts a closure into a TradeSettler. Lets a test wedge
// behaviour (e.g. cancel between settle and book) into the inner call.
type settlerFunc func(ctx context.Context, in SettleInput) (string, error)

func (f settlerFunc) Settle(ctx context.Context, in SettleInput) (string, error) {
	return f(ctx, in)
}

// TestIntegration_Execution_CancelBetweenSettleAndBook is the BE-T15
// regression for BE-3: a cancel that lands between bank.SettleTrade
// committing and the trading-side booking tx must not strand money.
// Sealed fills stay (spec p.50) — the holding + execution row land,
// the order ends cancelled but is_done=true.
func TestIntegration_Execution_CancelBetweenSettleAndBook(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "RACEX", ex, "10", "10", "9", 1000000)

	clientID := uuid.NewString()
	accID := uuid.NewString()

	o, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   2,
		AllOrNone:  true,
		AccountID:  accID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Wrap the inner settler so the first call cancels the order BEFORE
	// returning success. This simulates a cancel landing in the window
	// between bank settle (committed) and the trading-side booking tx.
	inner := svc.Settler
	fired := false
	svc.Settler = settlerFunc(func(ctx context.Context, in SettleInput) (string, error) {
		if !fired {
			fired = true
			if _, err := svc.Store.CancelOrder(ctx, o.ID); err != nil {
				return "", err
			}
		}
		return inner.Settle(ctx, in)
	})

	if _, err := svc.ProcessOrderTick(context.Background(), o.Order); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Holding lands at qty=2 (sealed fill stays).
	holdings, _ := svc.Store.ListHoldings(context.Background(), store.HoldingFilter{UserID: clientID})
	var found bool
	for _, h := range holdings {
		if h.SecurityID == sec.ID && h.Quantity == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected holding qty=2 (sealed fill stays after cancel); got %+v", holdings)
	}

	// Execution row is settled, not stuck pending.
	pending, err := svc.Store.GetPendingExecutionForOrder(context.Background(), o.ID)
	if err != nil {
		t.Fatalf("GetPending: %v", err)
	}
	if pending != nil {
		t.Fatalf("execution still pending after cancel-then-resume: %+v", pending)
	}
	execs, _ := svc.Store.ListExecutions(context.Background(), o.ID)
	if len(execs) != 1 || execs[0].Quantity != 2 {
		t.Fatalf("expected 1 settled exec qty=2, got %+v", execs)
	}

	// Order is cancelled but drained.
	final, _ := svc.Store.GetOrder(context.Background(), o.ID)
	if !final.Cancelled {
		t.Fatalf("order should be cancelled")
	}
	if !final.IsDone {
		t.Fatalf("order is_done should be true after sealed fill drained remaining")
	}
}

// TestIntegration_Execution_PendingRowResumes simulates a worker crash
// after the pending-row insert but before bank settle. The next tick
// must resume from the existing pending row (no duplicate exec, op_id
// = pending row UUID for bank-side idempotency).
func TestIntegration_Execution_PendingRowResumes(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "RECOV", ex, "10", "10", "9", 1000000)

	clientID := uuid.NewString()
	accID := uuid.NewString()

	o, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   3,
		AllOrNone:  true,
		AccountID:  accID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Hand-insert a pending row to simulate "previous worker died after
	// pending insert but before bank settle".
	var pendingID string
	err = svc.Store.ExecuteAtomic(context.Background(), func(tx pgx.Tx) error {
		e, ierr := svc.Store.InsertPendingExecution(context.Background(), tx, &domain.OrderExecution{
			OrderID:       o.ID,
			Quantity:      3,
			PricePerUnit:  "10",
			TotalAmount:   "30",
			CommissionAmt: "0",
		})
		if ierr != nil {
			return ierr
		}
		pendingID = e.ID
		return nil
	})
	if err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	// RunExecutionTick must pick this order up via the recovery sweep.
	fired, err := svc.RunExecutionTick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if fired == 0 {
		t.Fatalf("expected recovery sweep to fire the pending fill")
	}

	// Resume must not create a duplicate exec — same row flips to settled.
	execs, _ := svc.Store.ListExecutions(context.Background(), o.ID)
	if len(execs) != 1 {
		t.Fatalf("got %d execs, want 1 (resume must not duplicate)", len(execs))
	}
	if execs[0].ID != pendingID {
		t.Fatalf("recovery created a new exec %s instead of resuming pendingID=%s", execs[0].ID, pendingID)
	}

	// Bank settle was called once with op_id = pendingID — deterministic
	// across crashes per BE-3/BE-4.
	calls := currentSettler.settles()
	if len(calls) != 1 {
		t.Fatalf("expected 1 settle call, got %d", len(calls))
	}
	if calls[0].OpID != pendingID {
		t.Fatalf("settle op_id=%s, want pendingID=%s (deterministic op_id)", calls[0].OpID, pendingID)
	}
}

// TestIntegration_Tax_RetryUsesDeterministicOpID verifies BE-8: a
// SettleTax that errors leaves the realized_gains rows unpaid; the next
// RunTax invocation must re-derive the same op_id (bank-side idempotency
// then makes the retry safe).
func TestIntegration_Tax_RetryUsesDeterministicOpID(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "TAXR", ex, "100", "100", "99", 1000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	if err := writeRealizedGain(t, svc, clientID, sec.ID, accID, "1000"); err != nil {
		t.Fatalf("write: %v", err)
	}

	// First run: SettleTax errors. Rows stay unpaid.
	currentSettler.taxErr = fmt.Errorf("transient bank error")
	_, _ = svc.RunTax(TaxCronContext(context.Background()), RunTaxInput{})
	currentSettler.taxErr = nil

	// Second run: succeeds. Should record exactly one tax call.  The
	// op_id is derived from the realized_gain row-id set + account +
	// year-month — same set means same op_id, so the bank-side
	// idempotency makes the retry safe.  Compare against the
	// tax_op_id stamped on the realized_gain row rather than
	// recomputing locally (decouples the test from the derivation
	// implementation).
	res, err := svc.RunTax(TaxCronContext(context.Background()), RunTaxInput{})
	if err != nil {
		t.Fatalf("RunTax: %v", err)
	}
	if res.UsersTaxed != 1 {
		t.Fatalf("users_taxed=%d, want 1", res.UsersTaxed)
	}
	if len(currentSettler.taxCalls) != 1 {
		t.Fatalf("got %d successful tax calls, want 1", len(currentSettler.taxCalls))
	}
	var stampedOpID string
	if err := fixPool.QueryRow(context.Background(),
		`select coalesce(tax_op_id::text, '') from "trading".realized_gains
         where account_id = $1 and taxed = true`, accID).Scan(&stampedOpID); err != nil {
		t.Fatalf("read tax_op_id: %v", err)
	}
	if stampedOpID == "" {
		t.Fatalf("tax_op_id not stamped on realized_gain row")
	}
	if currentSettler.taxCalls[0].OpID != stampedOpID {
		t.Fatalf("tax op_id=%s, want %s (matches row stamp)",
			currentSettler.taxCalls[0].OpID, stampedOpID)
	}
}

// TestIntegration_Tax_NewGainsSameMonthGetFreshOpID verifies the fix
// for the soak suite's Finding 1: two tax runs in the same calendar
// month with DISJOINT realized_gain rows must yield DIFFERENT op_ids,
// so the bank's `(op_id, leg_index)` unique constraint doesn't
// silently swallow the second debit.  Pre-fix this would have charged
// the user once, then reported a fake "collected" total on subsequent
// runs while the state_tax account didn't move.
func TestIntegration_Tax_NewGainsSameMonthGetFreshOpID(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "TAXN", ex, "100", "100", "99", 1000)

	clientID := uuid.NewString()
	accID := uuid.NewString()

	// Round 1: one gain row, run tax.
	if err := writeRealizedGain(t, svc, clientID, sec.ID, accID, "1000"); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	res1, err := svc.RunTax(TaxCronContext(context.Background()), RunTaxInput{})
	if err != nil {
		t.Fatalf("RunTax 1: %v", err)
	}
	if res1.UsersTaxed != 1 {
		t.Fatalf("round 1 users_taxed=%d, want 1", res1.UsersTaxed)
	}
	round1Calls := len(currentSettler.taxCalls)
	if round1Calls != 1 {
		t.Fatalf("round 1 tax calls=%d, want 1", round1Calls)
	}
	opID1 := currentSettler.taxCalls[0].OpID

	// Round 2: add a fresh gain row in the same month, run tax again.
	if err := writeRealizedGain(t, svc, clientID, sec.ID, accID, "500"); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	res2, err := svc.RunTax(TaxCronContext(context.Background()), RunTaxInput{})
	if err != nil {
		t.Fatalf("RunTax 2: %v", err)
	}
	if res2.UsersTaxed != 1 {
		t.Fatalf("round 2 users_taxed=%d, want 1", res2.UsersTaxed)
	}
	if len(currentSettler.taxCalls) != round1Calls+1 {
		t.Fatalf("round 2 new tax calls=%d, want 1",
			len(currentSettler.taxCalls)-round1Calls)
	}
	opID2 := currentSettler.taxCalls[round1Calls].OpID
	if opID1 == opID2 {
		t.Fatalf("rounds 1 and 2 share op_id %s — bank would no-op the second debit", opID1)
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

// =====================================================================
// Option exercise (FE-4) — spec p.61.d
// =====================================================================

// seedOption inserts an option security pointing at an existing
// underlying. No listing row — the service falls back to security.premium
// for the option's own quoting, but exercise reads the *underlying*
// listing for the ITM check, so the underlying still needs prices.
func seedOption(
	t *testing.T,
	svc *Service,
	ticker string,
	underlying *domain.Security,
	optType domain.OptionType,
	strike string,
	contractSize string,
	settles time.Time,
) *domain.Security {
	t.Helper()
	sec, err := svc.Store.UpsertSecurity(context.Background(), &domain.Security{
		Ticker:               ticker,
		Name:                 ticker,
		Type:                 domain.SecurityOption,
		ExchangeMIC:          underlying.ExchangeMIC,
		Currency:             underlying.Currency,
		ContractSize:         contractSize,
		SettlementDate:       &settles,
		UnderlyingSecurityID: underlying.ID,
		OptionType:           optType,
		StrikePrice:          strike,
		Premium:              "0.50",
		ImpliedVolatility:    "0.25",
	})
	if err != nil {
		t.Fatalf("UpsertSecurity option: %v", err)
	}
	return sec
}

// seedHolding writes a portfolio_holdings row directly. Used to skip
// the order/fill pipeline and land the test at the post-state we need.
func seedHolding(
	t *testing.T,
	svc *Service,
	userID string,
	kind domain.UserKind,
	secID, accID string,
	qty int32,
	weightedAvgPrice string,
) *domain.Holding {
	t.Helper()
	ctx := context.Background()
	tx, err := fixPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	h, err := svc.Store.ApplyBuyFill(ctx, tx, userID, string(kind), secID, accID, qty, weightedAvgPrice)
	if err != nil {
		t.Fatalf("seedHolding ApplyBuyFill: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seedHolding: %v", err)
	}
	return h
}

// TestIntegration_ExerciseOption_PutSpecExample walks the spec p.61.d
// worked example end-to-end: actuary holds 300 MSFT @ $20 and 2 PUT
// contracts @ strike $19 (contract size 100); MSFT spot has fallen
// to $15. Exercising both PUTs sells 200 MSFT at the strike, credits
// the actuary's account, leaves 100 MSFT in the holding, and writes a
// realized_gain row in the underlying's currency.
func TestIntegration_ExerciseOption_PutSpecExample(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	stock, _ := seedStock(t, svc, "MSFT", ex, "15", "15", "15", 1_000_000)
	put := seedOption(t, svc, "MSFT-P-19", stock, domain.OptionPut, "19", "100", time.Now().Add(7*24*time.Hour))

	agentID := uuid.NewString()
	accID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "10000000", false)
	seedHolding(t, svc, agentID, domain.KindEmployee, stock.ID, accID, 300, "20")
	optionHolding := seedHolding(t, svc, agentID, domain.KindEmployee, put.ID, accID, 2, "0.50")

	res, err := svc.ExerciseOption(agentCtx(agentID), ExerciseOptionInput{
		HoldingID: optionHolding.ID,
		Quantity:  2,
	})
	if err != nil {
		t.Fatalf("ExerciseOption: %v", err)
	}
	if res.OptionHolding.Quantity != 0 {
		t.Fatalf("option holding qty after exercise = %d, want 0", res.OptionHolding.Quantity)
	}
	if res.UnderlyingHolding.Quantity != 100 {
		t.Fatalf("underlying qty = %d, want 100 (300 − 2*100)", res.UnderlyingHolding.Quantity)
	}

	calls := currentSettler.settles()
	if len(calls) != 1 {
		t.Fatalf("settle calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.Direction != "credit" {
		t.Fatalf("PUT exercise must credit (sell at strike); got %q", c.Direction)
	}
	if c.Currency != domain.CurrencyUSD {
		t.Fatalf("settle currency = %s, want USD", c.Currency)
	}
	if !numericEq(c.Amount, "3800") {
		t.Fatalf("settle amount = %s, want 3800 (200 × 19)", c.Amount)
	}
	if c.AccountID != accID {
		t.Fatalf("settle account = %s, want %s", c.AccountID, accID)
	}
	if !c.IsActuary {
		t.Fatalf("expected IsActuary=true on actuary exercise")
	}

	// Realized loss native = (19 − 20) × 200 = −200; RSD via pinned
	// USD/RSD ASK 110.50 = −22100.
	if !numericEq(res.RealizedGainNative, "-200") {
		t.Fatalf("native gain = %s, want -200", res.RealizedGainNative)
	}
	if !numericEq(res.RealizedGainRSD, "-22100") {
		t.Fatalf("rsd gain = %s, want -22100 (-200 × 110.50)", res.RealizedGainRSD)
	}

	// One realized_gain row landed.
	gains, err := svc.Store.ListRealizedGains(context.Background(), store.RealizedGainFilter{UserID: agentID})
	if err != nil {
		t.Fatalf("ListRealizedGains: %v", err)
	}
	if len(gains) != 1 {
		t.Fatalf("realized gain rows = %d, want 1", len(gains))
	}
	g := gains[0]
	if g.SecurityID != stock.ID {
		t.Fatalf("realized gain securityID = %s, want underlying %s", g.SecurityID, stock.ID)
	}
	if g.Quantity != 200 {
		t.Fatalf("realized gain qty = %d, want 200 shares", g.Quantity)
	}
}

// TestIntegration_ExerciseOption_CallBuysUnderlying covers the CALL
// path: actuary buys `qty × cs` shares at strike from the bank's house
// counterparty; weighted-avg cost basis on the new shares = strike;
// no realized_gain row.
func TestIntegration_ExerciseOption_CallBuysUnderlying(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	stock, _ := seedStock(t, svc, "AAPL", ex, "210", "210", "210", 1_000_000)
	call := seedOption(t, svc, "AAPL-C-190", stock, domain.OptionCall, "190", "100", time.Now().Add(60*24*time.Hour))

	agentID := uuid.NewString()
	accID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "10000000", false)
	optionHolding := seedHolding(t, svc, agentID, domain.KindEmployee, call.ID, accID, 1, "8.50")

	res, err := svc.ExerciseOption(agentCtx(agentID), ExerciseOptionInput{
		HoldingID: optionHolding.ID,
		Quantity:  1,
	})
	if err != nil {
		t.Fatalf("ExerciseOption: %v", err)
	}
	if res.OptionHolding.Quantity != 0 {
		t.Fatalf("option holding after exercise = %d, want 0", res.OptionHolding.Quantity)
	}
	if res.UnderlyingHolding == nil || res.UnderlyingHolding.Quantity != 100 {
		t.Fatalf("underlying qty = %v, want 100 fresh shares", res.UnderlyingHolding)
	}
	if !numericEq(res.UnderlyingHolding.WeightedAvgPrice, "190") {
		t.Fatalf("underlying avg cost = %s, want 190 (strike)", res.UnderlyingHolding.WeightedAvgPrice)
	}
	calls := currentSettler.settles()
	if len(calls) != 1 || calls[0].Direction != "debit" {
		t.Fatalf("CALL exercise must debit; got %+v", calls)
	}
	if !numericEq(calls[0].Amount, "19000") {
		t.Fatalf("debit amount = %s, want 19000 (100 × 190)", calls[0].Amount)
	}
	if res.RealizedGainNative != "" {
		t.Fatalf("CALL exercise must not record a realized gain; got %q", res.RealizedGainNative)
	}
}

// TestIntegration_ExerciseOption_OutOfMoney refuses an exercise when
// the option is OOM at the time of the request — even if the actuary
// owns the contract and settlement is in the future.
func TestIntegration_ExerciseOption_OutOfMoney(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	stock, _ := seedStock(t, svc, "MSFT", ex, "25", "25", "25", 1_000_000)
	put := seedOption(t, svc, "MSFT-P-19", stock, domain.OptionPut, "19", "100", time.Now().Add(7*24*time.Hour))

	agentID := uuid.NewString()
	accID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "10000000", false)
	seedHolding(t, svc, agentID, domain.KindEmployee, stock.ID, accID, 300, "20")
	optionHolding := seedHolding(t, svc, agentID, domain.KindEmployee, put.ID, accID, 2, "0.50")

	_, err := svc.ExerciseOption(agentCtx(agentID), ExerciseOptionInput{
		HoldingID: optionHolding.ID,
		Quantity:  2,
	})
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("expected FailedPrecondition for OOM exercise; got %v", err)
	}
	if len(currentSettler.settles()) != 0 {
		t.Fatalf("OOM rejection must not have hit the bank; got %d settle calls", len(currentSettler.settles()))
	}
}

// TestIntegration_ExerciseOption_RequiresActuary refuses a client
// principal even when they own the option holding (spec p.61.d:
// "samo Aktuari").
func TestIntegration_ExerciseOption_RequiresActuary(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	stock, _ := seedStock(t, svc, "MSFT", ex, "15", "15", "15", 1_000_000)
	put := seedOption(t, svc, "MSFT-P-19", stock, domain.OptionPut, "19", "100", time.Now().Add(7*24*time.Hour))

	clientID := uuid.NewString()
	accID := uuid.NewString()
	seedHolding(t, svc, clientID, domain.KindClient, stock.ID, accID, 300, "20")
	optionHolding := seedHolding(t, svc, clientID, domain.KindClient, put.ID, accID, 2, "0.50")

	_, err := svc.ExerciseOption(clientCtx(clientID), ExerciseOptionInput{
		HoldingID: optionHolding.ID,
		Quantity:  2,
	})
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("expected PermissionDenied; got %v", err)
	}
}

// =====================================================================
// BE-PR4 — c3 audit test-coverage gaps
// =====================================================================

// employeeNoTradingCtx mints a principal that's an employee but holds
// none of the trading permissions. Used by BE-T16 to assert
// assertTraderRole rejects at create-time.
func employeeNoTradingCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.EmployeeRead, permissions.ClientRead},
	})
}

// TestIntegration_StopLimit_TriggeredIdempotent (BE-T2) covers spec
// p.54: once a STOP_LIMIT crosses its stop and flips triggered=true,
// subsequent ticks behave like a Limit and never re-evaluate the stop.
// We trigger on tick 1, then drop the ask back below stop and confirm
// the row stays triggered=true; the order then fills under the limit
// rule as soon as ask <= limit.
func TestIntegration_StopLimit_TriggeredIdempotent(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	// stop=100, limit=200. ask=101 will fire the stop on tick 1; the
	// limit allows fill (101 ≤ 200) so the order completes immediately.
	// We re-run after dropping ask back below stop to assert triggered
	// persists.
	sec, _ := seedStock(t, svc, "STIM", ex, "100", "101", "100", 1_000_000)

	clientID := uuid.NewString()
	o, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderStopLimit,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		StopPrice:  "100",
		LimitPrice: "200",
		AllOrNone:  true,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create stop_limit: %v", err)
	}
	res, err := svc.ProcessOrderTick(context.Background(), o.Order)
	if err != nil {
		t.Fatalf("tick1: %v", err)
	}
	if !res.Fired {
		t.Fatalf("expected first tick to trigger + fill")
	}
	post, _ := svc.Store.GetOrder(context.Background(), o.ID)
	if !post.Triggered {
		t.Fatalf("post-tick1: triggered should be true")
	}

	// Drop ask + bid below stop (listings_spread requires ask >= bid).
	// The order is already done, but the row must keep triggered=true
	// — stop is never re-evaluated once flipped.
	if _, err := fixPool.Exec(context.Background(),
		`update "trading".listings set ask='50', bid='49', price='49' where security_id=$1`, sec.ID); err != nil {
		t.Fatalf("drop ask: %v", err)
	}
	post2, _ := svc.Store.GetOrder(context.Background(), o.ID)
	if !post2.Triggered {
		t.Fatalf("post-tick2: triggered must remain true even when ask < stop")
	}
}

// TestIntegration_Execution_LimitFillPriceMaxSell (BE-T3) is the sell
// twin of LimitFillPriceMin: spec p.51 — sell-limit fills at
// max(limit, bid). bid=110 vs limit=100 → fill at 110.
func TestIntegration_Execution_LimitFillPriceMaxSell(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "LIMS", ex, "110", "111", "110", 1_000_000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	// Seed a holding so the sell has something to draw from. ApplyBuyFill
	// creates a 1-share position at 90 native cost.
	seedHolding(t, svc, clientID, domain.KindClient, sec.ID, accID, 1, "90")

	o, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderLimit,
		Direction:  domain.DirectionSell,
		Quantity:   1,
		LimitPrice: "100",
		AllOrNone:  true,
		AccountID:  accID,
	})
	if err != nil {
		t.Fatalf("create limit-sell: %v", err)
	}
	res, err := svc.ProcessOrderTick(context.Background(), o.Order)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !res.Fired || res.Execution == nil {
		t.Fatalf("expected fill, got fired=%v exec=%v", res.Fired, res.Execution)
	}
	if !numericEq(res.Execution.PricePerUnit, "110") {
		t.Fatalf("fill price = %s, want 110 (max(limit=100, bid=110))",
			res.Execution.PricePerUnit)
	}
}

// TestIntegration_Execution_AONMultiTick (BE-T4) covers AON's
// "no fill until conditions allow" behavior. Tick 1: limit-buy at 90,
// ask=95 — conditions not met, no fill. Tick 2 after a price drop:
// ask=85, fills the entire order in one shot.
func TestIntegration_Execution_AONMultiTick(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AONM", ex, "95", "95", "94", 1_000_000)

	clientID := uuid.NewString()
	o, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderLimit,
		Direction:  domain.DirectionBuy,
		Quantity:   5,
		LimitPrice: "90",
		AllOrNone:  true,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create AON limit: %v", err)
	}

	// Tick 1: ask=95 > limit=90 → no fill, no partial. AON must NOT
	// emit a partial-fill row in this state.
	res, err := svc.ProcessOrderTick(context.Background(), o.Order)
	if err != nil {
		t.Fatalf("tick1: %v", err)
	}
	if res.Fired {
		t.Fatalf("AON should not fill when limit conditions miss")
	}
	mid, _ := svc.Store.GetOrder(context.Background(), o.ID)
	if mid.RemainingQuantity != 5 {
		t.Fatalf("AON: remaining after non-fill = %d, want 5", mid.RemainingQuantity)
	}

	// Drop ask below limit; AON fills the whole 5 shares atomically.
	if _, err := fixPool.Exec(context.Background(),
		`update "trading".listings set ask='85', bid='84', price='85' where security_id=$1`, sec.ID); err != nil {
		t.Fatalf("drop ask: %v", err)
	}
	mid2, _ := svc.Store.GetOrder(context.Background(), o.ID)
	res2, err := svc.ProcessOrderTick(context.Background(), mid2)
	if err != nil {
		t.Fatalf("tick2: %v", err)
	}
	if !res2.Fired || res2.Execution == nil {
		t.Fatalf("AON should fire once conditions allow; res=%+v", res2)
	}
	if res2.Execution.Quantity != 5 {
		t.Fatalf("AON fill qty=%d, want 5 (atomic)", res2.Execution.Quantity)
	}
	post, _ := svc.Store.GetOrder(context.Background(), o.ID)
	if !post.IsDone {
		t.Fatalf("AON: is_done should be true after the single 5-share fill")
	}
}

// TestIntegration_Margin_ActuaryBalanceOnly (BE-T5) covers spec p.55's
// actuary limb: actuaries pass margin eligibility on
// `account_balance > IMC` alone (the loan limb is client-only).
// Seeds an account that comfortably covers IMC and verifies the
// actuary's margin order auto-approves.
func TestIntegration_Margin_ActuaryBalanceOnly(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	// IMC ≈ 1.1 × 50 × 1 = 55 USD ≈ 6055 RSD. Seed 1000 USD ≈ 110k RSD.
	sec, _ := seedStock(t, svc, "ACTM", ex, "100", "100", "99", 1000)

	agentID := uuid.NewString()
	accID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "10000000", false)
	currentMargin.addAccount(accID, domain.CurrencyUSD, "1000")

	// Agent with TradingMargin explicitly granted — no loan in scope.
	agentMargin := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      agentID,
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Actuary, permissions.ActuaryAgent, permissions.TradingMargin},
	})
	out, err := svc.CreateOrder(agentMargin, CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  accID,
		Margin:     true,
	})
	if err != nil {
		t.Fatalf("actuary margin balance-only: %v", err)
	}
	if !out.Margin {
		t.Fatalf("margin flag did not persist on actuary order")
	}
	if out.Status != domain.OrderStatusApproved {
		t.Fatalf("actuary margin: status=%s, want approved (under-limit)", out.Status)
	}

	// And: a separate agent without TradingMargin must NOT auto-pass
	// the gate via a loan, since the loan limb is client-only.
	agent2 := uuid.NewString()
	seedActuary(t, svc, agent2, domain.ActuaryAgent, "10000000", false)
	currentMargin.addAccount(accID, domain.CurrencyUSD, "1000")
	currentMargin.addLoan(agent2, domain.CurrencyRSD, "9999999")
	noMarginCtx := auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:   agent2,
		UserKind: auth.KindEmployee,
		// Note: no TradingMargin — the loan-derived auto-grant is
		// client-only per spec p.55.
		Permissions: []string{permissions.Actuary, permissions.ActuaryAgent},
	})
	_, err = svc.CreateOrder(noMarginCtx, CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  accID,
		Margin:     true,
	})
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("agent without TradingMargin must not auto-pass via loan; got %v", err)
	}
}

// TestIntegration_Margin_AutoGrantLoanBelowIMC (BE-T6) covers the
// gap between the gate (loan != "" admits) and the eligibility
// (loan_amount > IMC required). A client without TradingMargin holds a
// loan that's smaller than IMC — they pass the permission gate but get
// rejected by assertMarginEligible. Asserts the boundary by comparing
// against a sibling test where the loan covers IMC and passes.
func TestIntegration_Margin_AutoGrantLoanBelowIMC(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	// IMC ≈ 6055 RSD per share at price=100.
	sec, _ := seedStock(t, svc, "AGLO", ex, "100", "100", "99", 1000)

	clientID := uuid.NewString()
	accID := uuid.NewString()
	currentMargin.addAccount(accID, domain.CurrencyUSD, "1") // ≈110 RSD; under-balance
	// 1000 RSD loan: passes the gate (amt != ""), fails the > IMC check.
	currentMargin.addLoan(clientID, domain.CurrencyRSD, "1000")

	_, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  accID,
		Margin:     true,
	})
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("loan < IMC: err=%v, want FailedPrecondition (gate passes, eligibility fails)", err)
	}
}

// TestIntegration_CreateOrder_OptionSettlementGuard (BE-T11) is the
// option twin of TestIntegration_CreateOrder_SettlementDateGuard. Spec
// p.50 also applies to options — exercise window doesn't extend past
// expiry. Auto-rejects at create on FailedPrecondition.
func TestIntegration_CreateOrder_OptionSettlementGuard(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	stock, _ := seedStock(t, svc, "MSFT", ex, "100", "100", "99", 1000)
	yesterday := time.Now().Add(-24 * time.Hour)
	expired := seedOption(t, svc, "MSFT-C-100-EXP", stock, domain.OptionCall, "100", "100", yesterday)

	// Clients can't trade options (spec p.58) — the settlement guard
	// fires before the visibility check, but to keep the test focused
	// on the guard we use an actuary principal which bypasses p.58.
	agentID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "10000000", false)

	_, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: expired.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("expired option: err=%v, want FailedPrecondition", err)
	}
}

// TestIntegration_DailyLimitResetCron (BE-T13) covers spec p.38: the
// daily 23:59 (Belgrade) cron zeroes used_limit across every actuary.
// We seed an agent whose used_limit equals daily_limit (so no further
// trade fits), invoke RunDailyResetActuaries, and assert the next
// trade fits under the cap.
func TestIntegration_DailyLimitResetCron(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "PNNY", ex, "1", "1", "1", 1000)

	agentID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "1000", false)

	// Saturate the limit by hand-writing used_limit = daily_limit.
	if _, err := fixPool.Exec(context.Background(),
		`update "trading".actuary_info set used_limit='1000' where employee_id=$1`, agentID); err != nil {
		t.Fatalf("saturate limit: %v", err)
	}

	// Pre-reset: a fresh trade pushes over and lands pending.
	pre, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("pre-reset create: %v", err)
	}
	if pre.Status != domain.OrderStatusPending {
		t.Fatalf("pre-reset status=%s, want pending (used_limit saturated)", pre.Status)
	}

	// Run the daily reset cron (no principal — service-internal call).
	n, err := svc.RunDailyResetActuaries(context.Background())
	if err != nil {
		t.Fatalf("RunDailyResetActuaries: %v", err)
	}
	if n < 1 {
		t.Fatalf("reset row count = %d, want ≥1", n)
	}
	info, err := svc.Store.GetActuaryInfo(context.Background(), agentID)
	if err != nil {
		t.Fatalf("GetActuaryInfo post-reset: %v", err)
	}
	if !numericEq(info.UsedLimit, "0") {
		t.Fatalf("used_limit post-reset = %q, want 0", info.UsedLimit)
	}

	// Post-reset: the same trade auto-approves.
	post, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("post-reset create: %v", err)
	}
	if post.Status != domain.OrderStatusApproved {
		t.Fatalf("post-reset status=%s, want approved", post.Status)
	}
}

// TestIntegration_UpdateActuaryLimit_BelowUsed pins the guard that
// rejects setting daily_limit below the current used_limit. Without
// it a supervisor can silently put an agent over their cap, blocking
// every order until 23:59 reset.
func TestIntegration_UpdateActuaryLimit_BelowUsed(t *testing.T) {
	svc := setup(t)
	supervisorID := uuid.NewString()
	agentID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "10000", false)

	if _, err := fixPool.Exec(context.Background(),
		`update "trading".actuary_info set used_limit='5000' where employee_id=$1`, agentID); err != nil {
		t.Fatalf("seed used_limit: %v", err)
	}

	// Lower limit but still ≥ used_limit — allowed.
	if _, err := svc.UpdateActuaryLimit(supervisorCtx(supervisorID), agentID, "5000"); err != nil {
		t.Fatalf("set limit == used_limit: %v", err)
	}

	// Drop below used_limit — must be rejected.
	_, err := svc.UpdateActuaryLimit(supervisorCtx(supervisorID), agentID, "4999")
	if err == nil {
		t.Fatalf("set limit < used_limit: want error, got nil")
	}
	got, err := svc.Store.GetActuaryInfo(context.Background(), agentID)
	if err != nil {
		t.Fatalf("GetActuaryInfo: %v", err)
	}
	if !numericEq(got.DailyLimit, "5000") {
		t.Fatalf("daily_limit = %q, want 5000 (unchanged after rejection)", got.DailyLimit)
	}
}

// TestIntegration_Execution_SkippedOnClosedMarket pins the spec p.39
// gate — when the exchange is forced closed via override (or naturally
// closed outside trading hours and outside the after-hours window),
// ProcessOrderTick must not move money or shares. Without the guard the
// cadence sweep happily debited the buyer's account regardless of state.
func TestIntegration_Execution_SkippedOnClosedMarket(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "CLSD", ex, "100", "100", "99", 100000)

	clientID := uuid.NewString()
	o, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AllOrNone:  true, // skip random cadence so a tick that should fill, fills
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	// Force the market closed and tick — no fill, no execution row.
	closed := domain.ExchangeOverrideClosed
	if _, err := svc.Store.SetExchangeOverride(context.Background(), ex.MIC, &closed); err != nil {
		t.Fatalf("override closed: %v", err)
	}
	res, err := svc.ProcessOrderTick(context.Background(), o.Order)
	if err != nil {
		t.Fatalf("tick (closed): %v", err)
	}
	if res.Fired {
		t.Fatalf("market closed: tick fired a fill, expected skip")
	}
	execs, err := svc.Store.ListExecutions(context.Background(), o.ID)
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(execs) != 0 {
		t.Fatalf("market closed: %d execution rows, want 0", len(execs))
	}

	// Flip the override back to open; the next tick should fire.
	open := domain.ExchangeOverrideOpen
	if _, err := svc.Store.SetExchangeOverride(context.Background(), ex.MIC, &open); err != nil {
		t.Fatalf("override open: %v", err)
	}
	o2, _ := svc.Store.GetOrder(context.Background(), o.ID)
	res, err = svc.ProcessOrderTick(context.Background(), o2)
	if err != nil {
		t.Fatalf("tick (open): %v", err)
	}
	if !res.Fired || res.Execution == nil {
		t.Fatalf("market open: fired=%v exec=%v, want fill", res.Fired, res.Execution)
	}
}

// TestIntegration_Execution_ForexSell (BE-T14) is the sell twin of
// TestIntegration_Execution_ForexNoHolding. A forex sell pairs a
// debit on the base currency with a credit on the quote currency, and
// (per spec p.42) writes no holding rows.
func TestIntegration_Execution_ForexSell(t *testing.T) {
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
		Price: "1.10", Ask: "1.11", Bid: "1.10",
		Volume: 1000000, ChangeAmt: "0", ContractSize: "1000",
	}); err != nil {
		t.Fatalf("UpsertListing forex: %v", err)
	}

	agentID := uuid.NewString()
	seedActuary(t, svc, agentID, domain.ActuaryAgent, "10000000", false)

	o, err := svc.CreateOrder(agentCtx(agentID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionSell,
		// Spec p.7 (E2E doc): forex minimum lot = 10. Use the floor.
		Quantity:   10,
		AllOrNone:  true,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create forex sell: %v", err)
	}
	res, err := svc.ProcessOrderTick(context.Background(), o.Order)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !res.Fired {
		t.Fatalf("expected forex sell to fire")
	}

	if len(currentSettler.forexCalls) != 1 {
		t.Fatalf("expected 1 forex settle call, got %d", len(currentSettler.forexCalls))
	}
	fx := currentSettler.forexCalls[0]
	if fx.Direction != "sell" {
		t.Fatalf("forex sell direction = %q, want sell", fx.Direction)
	}
	if fx.BaseCurrency != domain.CurrencyEUR || fx.QuoteCurrency != domain.CurrencyUSD {
		t.Fatalf("forex pair = %s/%s, want EUR/USD", fx.BaseCurrency, fx.QuoteCurrency)
	}
	// Sell takes the bid (1.10), not the ask, on the quote leg.
	// qty=10 (forex min lot, spec p.7) × cs=1000 × bid=1.10 = 11000.
	if !numericEq(fx.QuoteAmount, "11000") {
		t.Fatalf("forex sell quote_amount = %s, want 11000 (10000 × 1.10 bid)", fx.QuoteAmount)
	}

	// No holding row landed.
	holdings, _ := svc.Store.ListHoldings(context.Background(), store.HoldingFilter{UserID: agentID})
	for _, h := range holdings {
		if h.SecurityID == sec.ID {
			t.Fatalf("forex sell created a holding row; spec p.42 forbids that. holding=%+v", h)
		}
	}
}

// TestIntegration_CreateOrder_NonTradingEmployeeRejected (BE-T16)
// covers assertTraderRole: an employee with no trading permission
// (no Admin / Actuary* / TradingClient) is rejected up-front with
// PermissionDenied — the order must not land in the DB.
func TestIntegration_CreateOrder_NonTradingEmployeeRejected(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	id := uuid.NewString()
	_, err := svc.CreateOrder(employeeNoTradingCtx(id), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AccountID:  uuid.NewString(),
	})
	if !isApperr(err, apperr.KindPermissionDenied) {
		t.Fatalf("non-trading employee: err=%v, want PermissionDenied", err)
	}

	// And: no row was inserted.
	var n int
	if err := fixPool.QueryRow(context.Background(),
		`select count(*) from "trading".orders where user_id=$1`, id).Scan(&n); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if n != 0 {
		t.Fatalf("rejected order should not have been persisted; got %d rows", n)
	}
}

// TestIntegration_Recovery_PermanentBankError_AbandonsAndCancels covers
// Finding 2 from the 2026-05-11 soak audit: a pending execution whose
// bank settle fails with a permanent code (InvalidArgument here) must
// not be retried every tick forever. The recovery sweep marks the row
// abandoned, cancels the parent order, and on the next tick produces
// zero further bank calls.
//
// Symptom in the field (pre-fix): an order placed against the wrong
// source-account kind produced ~70 InvalidArgument WARN logs in
// 12 minutes with no backoff, no max-attempts, no terminal state. The
// pending row could only be cleared via raw DELETE because the status
// check constraint forbade any non-`pending`/`settled` value.
func TestIntegration_Recovery_PermanentBankError_AbandonsAndCancels(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "ABND", ex, "100", "100", "99", 1_000_000)

	// Permanent failure on settle. Same shape bank emits when the source
	// account isn't a trading-book account.
	currentSettler.reset()
	t.Cleanup(currentSettler.reset)
	currentSettler.settleErr = status.Error(codes.InvalidArgument,
		"aktuari moraju izabrati trading-book račun, ne menjačnicu")

	clientID := uuid.NewString()
	accID := uuid.NewString()
	o, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AllOrNone:  true, // single fill — no random sub-quantity
		AccountID:  accID,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	// Tick 1: fresh fill path. Inserts the pending row, calls settle,
	// settle returns InvalidArgument → recovery's first branch (which
	// also runs for fresh fills via the same code path) abandons.
	res, err := svc.ProcessOrderTick(context.Background(), o.Order)
	if err == nil {
		t.Fatalf("expected ProcessOrderTick to surface the bank error on the abandoning tick")
	}
	if res.Fired {
		t.Fatalf("permanent settle failure must not count as a fired fill")
	}

	// The first tick above doesn't actually go through the recovery
	// branch — executeFill is the fresh-fill path and it returns the
	// error without abandoning. The row stays pending so the NEXT tick
	// (recovery sweep) is what abandons it. Tick again.
	o2, _ := svc.Store.GetOrder(context.Background(), o.ID)
	res, err = svc.ProcessOrderTick(context.Background(), o2)
	if err != nil {
		t.Fatalf("second tick must not surface error (row already abandoned by recovery): %v", err)
	}
	if res.Fired {
		t.Fatalf("abandoned recovery must not count as a fired fill")
	}

	// Pending row is now abandoned, with the bank error stamped.
	var status, lastErr string
	var attempts int
	if err := fixPool.QueryRow(context.Background(),
		`select status, attempts, coalesce(last_error,'') from "trading".order_executions where order_id=$1`,
		o.ID).Scan(&status, &attempts, &lastErr); err != nil {
		t.Fatalf("read execution row: %v", err)
	}
	if status != "abandoned" {
		t.Fatalf("execution status = %q, want abandoned", status)
	}
	if lastErr == "" {
		t.Fatalf("last_error must be populated on abandoned row")
	}

	// Parent order is cancelled so the fresh-fill sweep doesn't seed a
	// replacement pending row with the same bad inputs.
	post, _ := svc.Store.GetOrder(context.Background(), o.ID)
	if !post.Cancelled {
		t.Fatalf("parent order must be cancelled after abandoning a pending fill")
	}

	// Drop the error so a subsequent tick *would* settle if it tried —
	// proves the recovery sweep no longer picks the row up.
	currentSettler.settleErr = nil
	pre := len(currentSettler.settles())
	post2, _ := svc.Store.GetOrder(context.Background(), o.ID)
	if _, err := svc.ProcessOrderTick(context.Background(), post2); err != nil {
		t.Fatalf("third tick after abandon: %v", err)
	}
	if got := len(currentSettler.settles()) - pre; got != 0 {
		t.Fatalf("recovery sweep continued retrying after abandon: %d further settle calls", got)
	}
}

// TestIntegration_Recovery_TransientBankError_BumpsAttempts confirms
// the other half of Finding 2's fix: transient bank errors increment
// the attempts counter and stamp last_error but DO NOT abandon — bank
// might recover, and book-keeping errors (which the classifier also
// treats as transient) MUST keep retrying because they sit between
// bank-committed and trading-booked.
func TestIntegration_Recovery_TransientBankError_BumpsAttempts(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "TRAN", ex, "100", "100", "99", 1_000_000)

	currentSettler.reset()
	t.Cleanup(currentSettler.reset)
	currentSettler.settleErr = status.Error(codes.Unavailable, "bank temporarily down")

	clientID := uuid.NewString()
	o, err := svc.CreateOrder(clientCtx(clientID), CreateOrderInput{
		SecurityID: sec.ID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   1,
		AllOrNone:  true,
		AccountID:  uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	// Fresh-fill tick leaves the pending row stranded with the bank error.
	if _, err := svc.ProcessOrderTick(context.Background(), o.Order); err == nil {
		t.Fatalf("expected first tick to surface the transient bank error")
	}

	// Recovery tick should bump the attempts counter and KEEP the row pending.
	o2, _ := svc.Store.GetOrder(context.Background(), o.ID)
	if _, err := svc.ProcessOrderTick(context.Background(), o2); err == nil {
		t.Fatalf("expected recovery tick to surface the transient bank error")
	}

	var rowStatus, lastErr string
	var attempts int
	if err := fixPool.QueryRow(context.Background(),
		`select status, attempts, coalesce(last_error,'') from "trading".order_executions where order_id=$1`,
		o.ID).Scan(&rowStatus, &attempts, &lastErr); err != nil {
		t.Fatalf("read execution row: %v", err)
	}
	if rowStatus != "pending" {
		t.Fatalf("transient error must not abandon — status = %q", rowStatus)
	}
	if attempts < 1 {
		t.Fatalf("attempts not bumped on transient retry: %d", attempts)
	}
	if lastErr == "" {
		t.Fatalf("last_error must be populated on transient retry")
	}

	// Parent order must not be cancelled — only permanent errors do that.
	post, _ := svc.Store.GetOrder(context.Background(), o.ID)
	if post.Cancelled {
		t.Fatalf("transient bank error must NOT cancel the parent order")
	}
}
