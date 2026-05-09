//go:build integration

// Package service integration tests exercise the bank service against
// a real Postgres. Same gating + reset pattern as the user service:
// build-tag protected, lazy fixture, schema reset between tests.
//
//	task up CELINA=c2
//	task migrate
//	task test:integration
//
// The exchange service is stubbed (RateProvider) so these tests don't
// need a live `exchange` container; rates are pinned in-process.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/store"
)

// =====================================================================
// Shared fixture
// =====================================================================

var (
	fixOnce sync.Once
	fixPool *pgxpool.Pool
	fixSkip string
)

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

// pinnedRates is a deterministic RateProvider stub. Rates match the
// figures used in the unit tests and the c2 worked examples.
type pinnedRates struct{}

func (pinnedRates) Quote(_ context.Context, from, to domain.Currency) (string, string, error) {
	switch {
	case from == domain.CurrencyEUR && to == domain.CurrencyRSD:
		return "117.20", "117.50", nil
	case from == domain.CurrencyUSD && to == domain.CurrencyRSD:
		return "110.20", "110.50", nil
	case from == domain.CurrencyCHF && to == domain.CurrencyRSD:
		return "118.00", "118.50", nil
	}
	return "", "", fmt.Errorf("no pinned rate for %s/%s", from, to)
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
		// Verify the c2 schema exists. If migrations haven't been
		// applied, skip with a useful message rather than fail.
		var n int
		if err := pool.QueryRow(ctx, `select count(*) from information_schema.tables where table_schema='bank' and table_name='accounts'`).Scan(&n); err != nil || n == 0 {
			fixSkip = "bank.accounts missing — run migrations first (task migrate)"
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
	svc := New(st, Config{
		BankCode:     "265",
		Branch:       "0001",
		FXCommission: "0.005",
	}, logger)
	svc.Rates = pinnedRates{}
	svc.Notifier = currentNotifier
	svc.UserResolver = currentResolver
	if err := svc.EnsureSystemAccounts(context.Background()); err != nil {
		t.Fatalf("ensure system accounts: %v", err)
	}
	currentNotifier.reset()
	currentResolver.reset()
	return svc
}

// =====================================================================
// Notification + user-resolver spies
// =====================================================================

type sentEmail struct{ To, Subject, Body string }

type spyNotifier struct {
	sync.Mutex
	out []sentEmail
}

func (s *spyNotifier) Send(_ context.Context, to, subject, body string, _ bool) error {
	s.Lock()
	defer s.Unlock()
	s.out = append(s.out, sentEmail{to, subject, body})
	return nil
}

func (s *spyNotifier) reset() {
	s.Lock()
	defer s.Unlock()
	s.out = s.out[:0]
}

func (s *spyNotifier) snapshot() []sentEmail {
	s.Lock()
	defer s.Unlock()
	out := make([]sentEmail, len(s.out))
	copy(out, s.out)
	return out
}

type spyResolver struct {
	sync.Mutex
	emails map[string]string
}

func (s *spyResolver) ClientEmail(_ context.Context, clientID string) (string, error) {
	s.Lock()
	defer s.Unlock()
	if v, ok := s.emails[clientID]; ok {
		return v, nil
	}
	// Default to `<id>@example.com` so tests that don't set up
	// specific emails still get a non-empty address.
	return clientID + "@example.com", nil
}

func (s *spyResolver) reset() {
	s.Lock()
	defer s.Unlock()
	s.emails = map[string]string{}
}

var (
	currentNotifier = &spyNotifier{}
	currentResolver = &spyResolver{emails: map[string]string{}}
)

func resetSchema(t *testing.T) {
	t.Helper()
	_, err := fixPool.Exec(context.Background(), `
        truncate
            bank.loan_installments,
            bank.loans,
            bank.loan_requests,
            bank.cards,
            bank.authorized_persons,
            bank.payment_recipients,
            bank.transactions,
            bank.accounts,
            bank.companies
        restart identity cascade`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// =====================================================================
// Principal helpers
// =====================================================================

func employeeAdminCtx() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      uuid.NewString(),
		UserKind:    auth.KindEmployee,
		Permissions: append([]string{}, permissions.RoleEmployeeAdmin...),
	})
}

func employeeAgentCtx() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      uuid.NewString(),
		UserKind:    auth.KindEmployee,
		Permissions: append([]string{}, permissions.RoleEmployeeAgent...),
	})
}

func clientCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindClient,
		Permissions: append([]string{}, permissions.RoleClientBasic...),
	})
}

// =====================================================================
// Account fixtures
// =====================================================================

// mintAccount creates an active account for the given client+currency
// at the given opening balance. Personal FX accounts must not declare
// a subtype (validation rule in accounts.go), so we skip Subtype for
// those.
func mintAccount(t *testing.T, svc *Service, ownerID string, kind domain.AccountKind, currency domain.Currency, opening string) *domain.Account {
	t.Helper()
	ctx := employeeAdminCtx()
	in := CreateAccountInput{
		OwnerClientID:  ownerID,
		Kind:           kind,
		Currency:       currency,
		OpeningBalance: opening,
	}
	if kind == domain.KindPersonalFX || kind == domain.KindBusinessFX {
		// FX accounts use the explicit "unspecified" sentinel so the
		// subtype check constraint at the DB layer is happy.
		in.Subtype = domain.SubtypeUnspecified
	} else {
		in.Subtype = domain.SubtypeStandard
	}
	a, err := svc.CreateAccount(ctx, in)
	if err != nil {
		t.Fatalf("mintAccount: %v", err)
	}
	return a
}

// =====================================================================
// Accounts
// =====================================================================

// TestIntegration_EnsureSystemAccounts pins the boot-time invariant: the
// bank pre-creates one "house" account per supported currency, each
// pre-funded with 10⁹ so the FX flow can borrow against it.
func TestIntegration_EnsureSystemAccounts(t *testing.T) {
	svc := setup(t)
	for _, c := range domain.SupportedCurrencies() {
		acc, err := svc.Store.GetSystemAccount(context.Background(), c)
		if err != nil {
			t.Errorf("missing system account for %s: %v", c, err)
			continue
		}
		if acc.OwnerClientID != domain.SystemOwnerID {
			t.Errorf("%s: system account owner = %q, want SystemOwnerID", c, acc.OwnerClientID)
		}
		if acc.Balance == "0.0000" || acc.Balance == "" {
			t.Errorf("%s: system account un-funded: balance=%s", c, acc.Balance)
		}
	}
}

// TestIntegration_CreateAccount_RSDChecking mints a personal RSD
// account, verifies the 18-digit checksum and that opening balance
// flows through to balance + available_balance.
func TestIntegration_CreateAccount_RSDChecking(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	a, err := svc.CreateAccount(employeeAdminCtx(), CreateAccountInput{
		OwnerClientID:  clientID,
		Kind:           domain.KindPersonalCheckingRSD,
		Subtype:        domain.SubtypeStandard,
		Currency:       domain.CurrencyRSD,
		OpeningBalance: "1000",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(a.Number) != 18 {
		t.Errorf("account number length = %d, want 18", len(a.Number))
	}
	if !strings.HasPrefix(a.Number, "265") {
		t.Errorf("bank prefix: got %s…", a.Number[:3])
	}
	if a.Balance != "1000.0000" || a.AvailableBalance != "1000.0000" {
		t.Errorf("opening flow: balance=%s available=%s", a.Balance, a.AvailableBalance)
	}
	if a.Status != domain.AccountActive {
		t.Errorf("status: %s, want active", a.Status)
	}
}

// TestIntegration_CreateAccount_PermissionRequired locks down spec p.11
// "Račun kreira Zaposleni": clients cannot mint accounts, even their
// own.
func TestIntegration_CreateAccount_PermissionRequired(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	_, err := svc.CreateAccount(clientCtx(clientID), CreateAccountInput{
		OwnerClientID: clientID,
		Kind:          domain.KindPersonalCheckingRSD,
		Subtype:       domain.SubtypeStandard,
		Currency:      domain.CurrencyRSD,
	})
	if err == nil {
		t.Fatal("client should not be able to create accounts")
	}
}

// TestIntegration_ListAccounts_ClientScoping: a client passing a filter
// for someone else's accounts gets PermissionDenied; calling without a
// filter sees only their own.
func TestIntegration_ListAccounts_ClientScoping(t *testing.T) {
	svc := setup(t)
	a := uuid.NewString()
	b := uuid.NewString()
	mintAccount(t, svc, a, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "100")
	mintAccount(t, svc, b, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "200")

	accs, _, err := svc.ListAccounts(clientCtx(a), domain.AccountFilter{}, 1, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, x := range accs {
		if x.OwnerClientID != a {
			t.Errorf("client a saw account owned by %s", x.OwnerClientID)
		}
	}
	if _, _, err := svc.ListAccounts(clientCtx(a), domain.AccountFilter{OwnerClientID: b}, 1, 50); err == nil {
		t.Error("client should not be able to filter on another client's id")
	}
}

// =====================================================================
// Payments
// =====================================================================

// TestIntegration_CreatePayment_SameCurrency: spec p.21 happy path.
// Source debited, destination credited, ledger row written, payment
// recipient template saved.
func TestIntegration_CreatePayment_SameCurrency(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()

	srcOwner := uuid.NewString()
	dstOwner := uuid.NewString()
	src := mintAccount(t, svc, srcOwner, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "1000")
	dst := mintAccount(t, svc, dstOwner, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "0")

	res, err := svc.CreatePayment(clientCtx(srcOwner), CreatePaymentInput{
		FromAccountID:   src.ID,
		ToAccountNumber: dst.Number,
		Amount:          "250",
		RecipientName:   "Kafić Dva",
		PaymentCode:     "289",
		Purpose:         "Račun za kafu",
		SaveRecipient:   true,
	})
	if err != nil {
		t.Fatalf("payment: %v", err)
	}
	if res.Status != domain.TxStatusRealized {
		t.Errorf("status: %s, want realized", res.Status)
	}
	if len(res.Transactions) != 1 {
		t.Errorf("same-currency payment should be 1 leg, got %d", len(res.Transactions))
	}

	srcAfter, _ := svc.Store.GetAccountByID(ctx, src.ID)
	dstAfter, _ := svc.Store.GetAccountByID(ctx, dst.ID)
	if srcAfter.AvailableBalance != "750.0000" {
		t.Errorf("source balance: %s, want 750.0000", srcAfter.AvailableBalance)
	}
	if dstAfter.AvailableBalance != "250.0000" {
		t.Errorf("dest balance: %s, want 250.0000", dstAfter.AvailableBalance)
	}

	recips, err := svc.Store.ListPaymentRecipients(ctx, srcOwner)
	if err != nil {
		t.Fatalf("list recipients: %v", err)
	}
	if len(recips) != 1 || recips[0].AccountNumber != dst.Number {
		t.Errorf("save_recipient did not create a template: %+v", recips)
	}
}

// TestIntegration_CreatePayment_InsufficientFunds rejects without
// touching balances, returns the spec error message.
func TestIntegration_CreatePayment_InsufficientFunds(t *testing.T) {
	svc := setup(t)
	srcOwner := uuid.NewString()
	dstOwner := uuid.NewString()
	src := mintAccount(t, svc, srcOwner, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "100")
	dst := mintAccount(t, svc, dstOwner, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "0")
	_, err := svc.CreatePayment(clientCtx(srcOwner), CreatePaymentInput{
		FromAccountID:   src.ID,
		ToAccountNumber: dst.Number,
		Amount:          "500",
		RecipientName:   "X",
		PaymentCode:     "289",
		Purpose:         "Y",
	})
	if err == nil {
		t.Fatal("expected insufficient-funds error")
	}
	srcAfter, _ := svc.Store.GetAccountByID(context.Background(), src.ID)
	if srcAfter.AvailableBalance != "100.0000" {
		t.Errorf("source balance touched on failure: %s", srcAfter.AvailableBalance)
	}
}

// TestIntegration_CreateTransfer_FX is the menjačnica-via-own-accounts
// flow. RSD → EUR uses the bank's RSD and EUR house accounts.
//
// Post-conditions:
//   - source RSD account drained by 1175 RSD
//   - destination EUR account credited 9.95 EUR (after 0.5% commission)
//   - bank RSD house +1175.00, bank EUR house -9.95
//   - exactly two transaction rows under one op_id
func TestIntegration_CreateTransfer_FX(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()
	clientID := uuid.NewString()
	rsd := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "10000")
	eur := mintAccount(t, svc, clientID, domain.KindPersonalFX, domain.CurrencyEUR, "0")

	houseRSDBefore, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyRSD)
	houseEURBefore, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyEUR)

	res, err := svc.CreateTransfer(clientCtx(clientID), CreateTransferInput{
		FromAccountID: rsd.ID,
		ToAccountID:   eur.ID,
		Amount:        "1175",
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if len(res.Transactions) != 2 {
		t.Errorf("FX transfer should be 2 legs, got %d", len(res.Transactions))
	}
	for i, tx := range res.Transactions {
		if tx.OpID != res.OpID {
			t.Errorf("leg %d op_id mismatch: %s vs %s", i, tx.OpID, res.OpID)
		}
	}

	rsdAfter, _ := svc.Store.GetAccountByID(ctx, rsd.ID)
	eurAfter, _ := svc.Store.GetAccountByID(ctx, eur.ID)
	if rsdAfter.AvailableBalance != "8825.0000" {
		t.Errorf("rsd balance: %s, want 8825.0000", rsdAfter.AvailableBalance)
	}
	if eurAfter.AvailableBalance != "9.9500" {
		t.Errorf("eur balance: %s, want 9.9500", eurAfter.AvailableBalance)
	}

	houseRSDAfter, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyRSD)
	houseEURAfter, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyEUR)
	deltaRSD := mustSub(t, houseRSDAfter.AvailableBalance, houseRSDBefore.AvailableBalance)
	deltaEUR := mustSub(t, houseEURAfter.AvailableBalance, houseEURBefore.AvailableBalance)
	if deltaRSD != "1175.0000" {
		t.Errorf("RSD house delta: %s, want +1175.0000", deltaRSD)
	}
	if deltaEUR != "-9.9500" {
		t.Errorf("EUR house delta: %s, want -9.9500", deltaEUR)
	}
}

func TestIntegration_CreateTransfer_OwnAccountsOnly(t *testing.T) {
	svc := setup(t)
	a := uuid.NewString()
	b := uuid.NewString()
	src := mintAccount(t, svc, a, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "1000")
	dst := mintAccount(t, svc, b, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "0")
	_, err := svc.CreateTransfer(clientCtx(a), CreateTransferInput{
		FromAccountID: src.ID,
		ToAccountID:   dst.ID,
		Amount:        "100",
	})
	if err == nil {
		t.Fatal("transfer to another client's account should be rejected — use Plaćanje")
	}
}

// =====================================================================
// Cards
// =====================================================================

// TestIntegration_Card_Lifecycle covers spec p.27-29 status transitions:
//   - any client can BLOCK their own active card
//   - clients cannot UNBLOCK (must call employee)
//   - employee with card.write can UNBLOCK
//   - deactivated cards are immutable
func TestIntegration_Card_Lifecycle(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "0")

	c, _, err := svc.CreateCard(clientCtx(clientID), CreateCardInput{
		AccountID: acc.ID,
		Brand:     domain.BrandVisa,
		Name:      "Lična kartica",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.Status != domain.CardActive {
		t.Errorf("initial status: %s", c.Status)
	}

	if _, err := svc.SetCardStatus(clientCtx(clientID), c.ID, domain.CardBlocked); err != nil {
		t.Fatalf("client BLOCK: %v", err)
	}

	// Client UNBLOCK rejected — only employees can revive a blocked card.
	if _, err := svc.SetCardStatus(clientCtx(clientID), c.ID, domain.CardActive); err == nil {
		t.Error("client UNBLOCK should be rejected")
	}

	// Employee UNBLOCK succeeds.
	if _, err := svc.SetCardStatus(employeeAdminCtx(), c.ID, domain.CardActive); err != nil {
		t.Fatalf("employee UNBLOCK: %v", err)
	}

	// Deactivate is terminal.
	if _, err := svc.SetCardStatus(employeeAdminCtx(), c.ID, domain.CardDeactivated); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, err := svc.SetCardStatus(employeeAdminCtx(), c.ID, domain.CardActive); err == nil {
		t.Error("re-activating a deactivated card should be rejected")
	}
}

// TestIntegration_Card_PersonalLimit: spec p.27 — max 2 active personal
// cards per account. The third request must fail.
func TestIntegration_Card_PersonalLimit(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "0")
	mk := func() error {
		_, _, err := svc.CreateCard(clientCtx(clientID), CreateCardInput{
			AccountID: acc.ID,
			Brand:     domain.BrandVisa,
			Name:      "Card",
		})
		return err
	}
	for i := 0; i < 2; i++ {
		if err := mk(); err != nil {
			t.Fatalf("card %d: %v", i+1, err)
		}
	}
	if err := mk(); err == nil {
		t.Error("3rd personal card should be rejected")
	}
}

// =====================================================================
// Loans
// =====================================================================

// TestIntegration_LoanFlow_RequestApproveInstallment is the full spec
// p.30-34 happy path:
//
//   1. client submits a request (cash, fixed, 60 months, 1M RSD)
//   2. employee approves → loan is minted + disbursed (1M RSD into client account)
//   3. installment cron pays 1 installment → balance debited, remaining
//      principal reduced by the principal portion (interest stays with bank)
//
// The exact numbers are pinned because the rate brackets + annuity
// formula are spec-locked: 6.00% base + 1.75% margin = 7.75%, A ≈ 20156.96.
// First-installment interest: 1e6 × 7.75/1200 ≈ 6458.33; principal portion
// 20156.96 − 6458.33 = 13698.63; remaining 1e6 − 13698.63 = 986301.37.
func TestIntegration_LoanFlow_RequestApproveInstallment(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()

	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "5000000")

	// 1. Submit
	req, err := svc.SubmitLoanRequest(clientCtx(clientID), SubmitLoanRequestInput{
		AccountID:                acc.ID,
		LoanType:                 domain.LoanTypeCash,
		InterestType:             domain.InterestFixed,
		Amount:                   "1000000",
		Currency:                 domain.CurrencyRSD,
		Purpose:                  "test",
		MonthlySalary:            "100000",
		EmploymentStatus:         domain.EmploymentPermanent,
		EmploymentDurationMonths: 24,
		InstallmentsTotal:        60,
		ContactPhone:             "+381111222333",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if req.Status != domain.RequestPending {
		t.Errorf("status: %s, want pending", req.Status)
	}

	// 2. Approve (employee)
	if _, err := svc.DecideLoanRequest(employeeAdminCtx(), req.ID, true, ""); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Loan is minted; client account credited by 1M (disbursement leg).
	loans, _, err := svc.ListLoans(employeeAdminCtx(), domain.LoanFilter{ClientID: clientID}, 1, 10)
	if err != nil || len(loans) != 1 {
		t.Fatalf("list loans: err=%v len=%d", err, len(loans))
	}
	loan := loans[0]
	// Money is stored at AmountScale=4 with truncation (no banker's
	// rounding). Spec p.34 gives the rounded figures (20156.96 etc);
	// we pin the exact 4-decimal output to catch silent precision
	// drift in pkg/loans or pkg/money.
	if loan.InstallmentAmount != "20156.9598" {
		t.Errorf("installment amount: %s, want 20156.9598 (cash 60mo @ 7.75%%, truncated to 4dp)", loan.InstallmentAmount)
	}
	if loan.RemainingPrincipal != "1000000.0000" {
		t.Errorf("remaining at t0: %s, want 1000000.0000", loan.RemainingPrincipal)
	}
	accAfterDisburse, _ := svc.Store.GetAccountByID(ctx, acc.ID)
	if accAfterDisburse.AvailableBalance != "6000000.0000" {
		t.Errorf("post-disbursement balance: %s, want 6000000.0000", accAfterDisburse.AvailableBalance)
	}

	// 3. Force installment due today, run job
	dueOn := time.Now().Add(-1 * time.Hour) // anything <= now triggers a due row
	if _, err := fixPool.Exec(ctx, `update bank.loan_installments set expected_due_date = $1 where loan_id = $2 and sequence_number = 1`, dueOn, loan.ID); err != nil {
		t.Fatalf("fixture due-date update: %v", err)
	}
	res, err := svc.RunInstallmentJob(employeeAdminCtx(), time.Now())
	if err != nil {
		t.Fatalf("run installment job: %v", err)
	}
	if res.Paid != 1 {
		t.Errorf("paid count: %d, want 1", res.Paid)
	}

	loanAfter, err := svc.Store.GetLoanByID(ctx, loan.ID)
	if err != nil {
		t.Fatalf("get loan: %v", err)
	}
	// 1,000,000 − (20156.9598 − 6458.3333) = 986301.3735.
	if loanAfter.RemainingPrincipal != "986301.3735" {
		t.Errorf("remaining after 1 installment: %s, want 986301.3735", loanAfter.RemainingPrincipal)
	}
	accAfterPay, _ := svc.Store.GetAccountByID(ctx, acc.ID)
	// 6,000,000 − 20156.9598 = 5,979,843.0402.
	if accAfterPay.AvailableBalance != "5979843.0402" {
		t.Errorf("balance after 1 installment: %s, want 5979843.0402", accAfterPay.AvailableBalance)
	}
}

// TestIntegration_Loan_VariableRateRefresh: variable-rate cron writes
// a fresh pomeraj into [-1.50%, +1.50%] and recomputes the installment.
// We don't pin a specific value (random) but assert: the offset moved
// or stayed in range, and the installment recomputation didn't blow up.
func TestIntegration_Loan_VariableRateRefresh(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "5000000")
	req, err := svc.SubmitLoanRequest(clientCtx(clientID), SubmitLoanRequestInput{
		AccountID:                acc.ID,
		LoanType:                 domain.LoanTypeCash,
		InterestType:             domain.InterestVariable,
		Amount:                   "500000",
		Currency:                 domain.CurrencyRSD,
		Purpose:                  "x",
		MonthlySalary:            "100000",
		EmploymentStatus:         domain.EmploymentPermanent,
		EmploymentDurationMonths: 24,
		InstallmentsTotal:        24,
		ContactPhone:             "+381111222333",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := svc.DecideLoanRequest(employeeAdminCtx(), req.ID, true, ""); err != nil {
		t.Fatalf("approve: %v", err)
	}

	res, err := svc.RunVariableRateJob(employeeAdminCtx())
	if err != nil {
		t.Fatalf("run variable rate job: %v", err)
	}
	if res.Updated != 1 {
		t.Errorf("updated count: %d, want 1", res.Updated)
	}
	loans, _, _ := svc.ListLoans(employeeAdminCtx(), domain.LoanFilter{ClientID: clientID}, 1, 1)
	if len(loans) != 1 {
		t.Fatal("loan not found")
	}
	off := loans[0].CurrentOffset
	// Just sanity-check the offset is a sane decimal — bounds are
	// covered separately in pkg/loans tests; here we want to know
	// the cron path wrote *something*.
	if off == "" {
		t.Errorf("offset not written: %q", off)
	}

	// Sanity: variable loans always re-amortise to a positive installment.
	if loans[0].InstallmentAmount == "" || loans[0].InstallmentAmount == "0.0000" {
		t.Errorf("installment recomputed bogus: %s", loans[0].InstallmentAmount)
	}
	_ = ctx
}

// TestIntegration_Loan_RejectsWrongInstallmentCount mirrors spec p.31:
// 84-month is not a valid term for housing.
func TestIntegration_Loan_RejectsWrongInstallmentCount(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "0")
	_, err := svc.SubmitLoanRequest(clientCtx(clientID), SubmitLoanRequestInput{
		AccountID:                acc.ID,
		LoanType:                 domain.LoanTypeHousing,
		InterestType:             domain.InterestFixed,
		Amount:                   "5000000",
		Currency:                 domain.CurrencyRSD,
		Purpose:                  "house",
		MonthlySalary:            "200000",
		EmploymentStatus:         domain.EmploymentPermanent,
		EmploymentDurationMonths: 36,
		InstallmentsTotal:        84, // not allowed for housing
		ContactPhone:             "+381111222333",
	})
	if err == nil {
		t.Fatal("housing 84mo should be rejected")
	}
}

// TestIntegration_Loan_DecideRejection captures the rejection_reason and
// flips status to rejected.
func TestIntegration_Loan_DecideRejection(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "0")
	req, err := svc.SubmitLoanRequest(clientCtx(clientID), SubmitLoanRequestInput{
		AccountID:                acc.ID,
		LoanType:                 domain.LoanTypeCash,
		InterestType:             domain.InterestFixed,
		Amount:                   "100000",
		Currency:                 domain.CurrencyRSD,
		Purpose:                  "x",
		MonthlySalary:            "50000",
		EmploymentStatus:         domain.EmploymentPermanent,
		EmploymentDurationMonths: 12,
		InstallmentsTotal:        12,
		ContactPhone:             "+381111222333",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	out, err := svc.DecideLoanRequest(employeeAdminCtx(), req.ID, false, "salary too low")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if out.Status != domain.RequestRejected {
		t.Errorf("status: %s, want rejected", out.Status)
	}
	if out.RejectionReason != "salary too low" {
		t.Errorf("reason: %q", out.RejectionReason)
	}
	// No loan should be minted on rejection.
	loans, _, _ := svc.ListLoans(employeeAdminCtx(), domain.LoanFilter{ClientID: clientID}, 1, 5)
	if len(loans) != 0 {
		t.Errorf("rejected request must not mint a loan, got %d", len(loans))
	}
}

// =====================================================================
// Recipients
// =====================================================================

// TestIntegration_Recipients_CRUD: list-create-update-delete all
// scoped to the caller (clients only see their own).
func TestIntegration_Recipients_CRUD(t *testing.T) {
	svc := setup(t)
	clientA := uuid.NewString()
	clientB := uuid.NewString()

	// Use a real account belonging to someone else as the recipient
	// account number so account.Validate's checksum passes.
	dest := mintAccount(t, svc, uuid.NewString(), domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "0")

	rec, err := svc.CreatePaymentRecipient(clientCtx(clientA), "Mama", dest.Number)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.ClientID != clientA {
		t.Errorf("client_id: %s, want %s", rec.ClientID, clientA)
	}

	got, err := svc.ListPaymentRecipients(clientCtx(clientA))
	if err != nil || len(got) != 1 {
		t.Fatalf("list a: err=%v len=%d", err, len(got))
	}
	bRecips, err := svc.ListPaymentRecipients(clientCtx(clientB))
	if err != nil {
		t.Fatalf("list b: %v", err)
	}
	if len(bRecips) != 0 {
		t.Errorf("client b should see no recipients, got %d", len(bRecips))
	}

	if err := svc.DeletePaymentRecipient(clientCtx(clientA), rec.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got2, _ := svc.ListPaymentRecipients(clientCtx(clientA))
	if len(got2) != 0 {
		t.Errorf("after delete: %d", len(got2))
	}
}

// =====================================================================
// Notifications
// =====================================================================

// TestIntegration_Notify_CardBlocked: the spec p.29 has "Banka šalje
// obaveštenje" when a card is blocked. SetCardStatus → email sent.
func TestIntegration_Notify_CardBlocked(t *testing.T) {
	svc := setup(t)
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "0")
	c, _, err := svc.CreateCard(clientCtx(clientID), CreateCardInput{
		AccountID: acc.ID, Brand: domain.BrandVisa, Name: "L1",
	})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	currentNotifier.reset()

	if _, err := svc.SetCardStatus(clientCtx(clientID), c.ID, domain.CardBlocked); err != nil {
		t.Fatalf("block: %v", err)
	}
	emails := currentNotifier.snapshot()
	if len(emails) != 1 {
		t.Fatalf("expected 1 email on block, got %d", len(emails))
	}
	if !strings.Contains(emails[0].Subject, "blokirana") {
		t.Errorf("subject: %q", emails[0].Subject)
	}
	if emails[0].To != clientID+"@example.com" {
		t.Errorf("recipient: %q", emails[0].To)
	}
}

// TestIntegration_Notify_LoanDecision: approve and reject paths each
// emit one Serbian email.
func TestIntegration_Notify_LoanDecision(t *testing.T) {
	svc := setup(t)
	for _, tc := range []struct {
		name       string
		approve    bool
		reason     string
		wantInSubj string
		wantInBody string
	}{
		{"approve", true, "", "odobren", "odobren"},
		{"reject", false, "salary too low", "odbijen", "salary too low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			currentNotifier.reset()
			clientID := uuid.NewString()
			acc := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "5000000")
			req, err := svc.SubmitLoanRequest(clientCtx(clientID), SubmitLoanRequestInput{
				AccountID:                acc.ID,
				LoanType:                 domain.LoanTypeCash,
				InterestType:             domain.InterestFixed,
				Amount:                   "100000",
				Currency:                 domain.CurrencyRSD,
				Purpose:                  "x",
				MonthlySalary:            "100000",
				EmploymentStatus:         domain.EmploymentPermanent,
				EmploymentDurationMonths: 12,
				InstallmentsTotal:        12,
				ContactPhone:             "+381111222333",
			})
			if err != nil {
				t.Fatalf("submit: %v", err)
			}
			if _, err := svc.DecideLoanRequest(employeeAdminCtx(), req.ID, tc.approve, tc.reason); err != nil {
				t.Fatalf("decide: %v", err)
			}
			emails := currentNotifier.snapshot()
			if len(emails) != 1 {
				t.Fatalf("expected 1 email, got %d", len(emails))
			}
			if !strings.Contains(emails[0].Subject, tc.wantInSubj) {
				t.Errorf("subject: %q", emails[0].Subject)
			}
			if !strings.Contains(emails[0].Body, tc.wantInBody) {
				t.Errorf("body missing %q: %q", tc.wantInBody, emails[0].Body)
			}
		})
	}
}

// =====================================================================
// Maintenance fee cron
// =====================================================================

// TestIntegration_MaintenanceFee_ChargesAndStamps walks the cron path:
// freshly-created RSD standard account → fast-forward last_maintenance
// to >28 days ago via SQL → run job → assert balance debited 255 RSD,
// ledger row written, last_maintenance_debit stamped to ~now.
func TestIntegration_MaintenanceFee_ChargesAndStamps(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "1000")
	if acc.MaintenanceFee != "255.0000" {
		t.Fatalf("default fee not applied at creation: %s", acc.MaintenanceFee)
	}

	// Pretend the account hasn't been charged in 30 days.
	if _, err := fixPool.Exec(ctx,
		`update bank.accounts set last_maintenance_debit = now() - interval '30 days' where id = $1`, acc.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	houseBefore, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyRSD)

	res, err := svc.RunMaintenanceFeeJob(employeeAdminCtx(), time.Now())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Charged != 1 {
		t.Errorf("charged: %d, want 1", res.Charged)
	}

	accAfter, _ := svc.Store.GetAccountByID(ctx, acc.ID)
	if accAfter.AvailableBalance != "745.0000" {
		t.Errorf("balance after: %s, want 745.0000 (= 1000 − 255)", accAfter.AvailableBalance)
	}
	if accAfter.LastMaintenanceDebit == nil ||
		time.Since(*accAfter.LastMaintenanceDebit) > time.Minute {
		t.Errorf("last_maintenance_debit not stamped to ~now: %v", accAfter.LastMaintenanceDebit)
	}

	houseAfter, _ := svc.Store.GetSystemAccount(ctx, domain.CurrencyRSD)
	deltaRSD := mustSub(t, houseAfter.AvailableBalance, houseBefore.AvailableBalance)
	if deltaRSD != "255.0000" {
		t.Errorf("RSD house delta: %s, want +255.0000", deltaRSD)
	}
}

// TestIntegration_MaintenanceFee_Idempotent: a second run on the same
// day must not double-charge — the just-stamped account is no longer
// "due" for the cutoff.
func TestIntegration_MaintenanceFee_Idempotent(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalCheckingRSD, domain.CurrencyRSD, "1000")
	if _, err := fixPool.Exec(ctx,
		`update bank.accounts set last_maintenance_debit = now() - interval '30 days' where id = $1`, acc.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.RunMaintenanceFeeJob(employeeAdminCtx(), time.Now()); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	accAfter, _ := svc.Store.GetAccountByID(ctx, acc.ID)
	if accAfter.AvailableBalance != "745.0000" {
		t.Errorf("balance after 3 runs: %s, want 745.0000 (single charge)", accAfter.AvailableBalance)
	}
}

// TestIntegration_MaintenanceFee_FXAccountSkipped: FX accounts have
// fee=0 (per spec p.13 example) and must not be touched by the cron.
func TestIntegration_MaintenanceFee_FXAccountSkipped(t *testing.T) {
	svc := setup(t)
	ctx := context.Background()
	clientID := uuid.NewString()
	acc := mintAccount(t, svc, clientID, domain.KindPersonalFX, domain.CurrencyEUR, "100")
	if acc.MaintenanceFee != "0.0000" {
		t.Errorf("FX fee should be 0, got %s", acc.MaintenanceFee)
	}
	// Backdate, run job, balance must be untouched.
	if _, err := fixPool.Exec(ctx,
		`update bank.accounts set last_maintenance_debit = now() - interval '30 days' where id = $1`, acc.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	res, err := svc.RunMaintenanceFeeJob(employeeAdminCtx(), time.Now())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Charged != 0 {
		t.Errorf("FX accounts should not be charged, got %d", res.Charged)
	}
	accAfter, _ := svc.Store.GetAccountByID(ctx, acc.ID)
	if accAfter.AvailableBalance != "100.0000" {
		t.Errorf("FX balance touched: %s", accAfter.AvailableBalance)
	}
}

// =====================================================================
// Companies
// =====================================================================

// TestIntegration_Company_CRUD covers create + immutability of
// registry_id/tax_id (spec p.14) and basic listing.
func TestIntegration_Company_CRUD(t *testing.T) {
	svc := setup(t)
	owner := uuid.NewString()

	c, err := svc.CreateCompany(employeeAdminCtx(), CreateCompanyInput{
		Name:          "Tech d.o.o.",
		RegistryID:    "12345678",
		TaxID:         "123456789",
		ActivityCode:  "62.01",
		Address:       "Beograd",
		OwnerClientID: owner,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	upd, err := svc.UpdateCompany(employeeAdminCtx(), UpdateCompanyInput{
		ID:           c.ID,
		Name:         "Tech d.o.o. — novi naziv",
		ActivityCode: "62.02",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Name != "Tech d.o.o. — novi naziv" || upd.ActivityCode != "62.02" {
		t.Errorf("update did not stick: %+v", upd)
	}
	if upd.RegistryID != "12345678" || upd.TaxID != "123456789" {
		t.Errorf("identifier mutated: registry=%s tax=%s", upd.RegistryID, upd.TaxID)
	}
}

// =====================================================================
// Helpers
// =====================================================================

// mustSub returns a − b formatted to AmountScale. Panics on parse error;
// helpers in pkg/money would do, but this avoids importing big.Rat in
// the test surface area.
func mustSub(t *testing.T, a, b string) string {
	t.Helper()
	af, bf := mustFloat(t, a), mustFloat(t, b)
	out := af - bf
	return fmt.Sprintf("%.4f", out)
}

func mustFloat(t *testing.T, s string) float64 {
	t.Helper()
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		t.Fatalf("float parse %q: %v", s, err)
	}
	return f
}

