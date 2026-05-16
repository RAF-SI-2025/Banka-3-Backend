// Command seed plants development fixtures across the whole stack.
// All layers are idempotent; every layer is unconditional except the
// investment-fund fixtures (layer 4's funds), which are gated by the
// SEED_FUNDS env switch (default on):
//
//  1. bootstrap admin (so the system has someone who can create
//     employees — the spec gates that on admin)
//  2. test client klijent@banka.local
//  3. bank fixtures hung off the test client: two companies, three
//     accounts (RSD personal / EUR personal / RSD business), two
//     cards (active Visa + blocked Mastercard), two loans (active +
//     paid_off), and two loan requests (pending + rejected)
//  4. trading fixtures (USD trading account, actuary + supervisor
//     employees, exchanges, securities, OTC threads) and three
//     investment funds managed by the supervisor
//
// Configuration:
//
//	DATABASE_URL         — required; standard pgx DSN
//	SEED_ADMIN_EMAIL     (default admin@banka.local)
//	SEED_ADMIN_PASSWORD  (default Admin123!) — must satisfy spec policy
//	SEED_ADMIN_USERNAME  (default admin)
//	SEED_CLIENT_EMAIL    (default klijent@banka.local)
//	SEED_CLIENT_PASSWORD (default Klijent123!)
//	SEED_CLIENT2_EMAIL    (default klijent2@banka.local)
//	SEED_CLIENT2_PASSWORD (default Klijent123!)
//	SEED_EMPLOYEE_EMAIL    (default zaposleni@banka.local)
//	SEED_EMPLOYEE_USERNAME (default zaposleni)
//	SEED_EMPLOYEE_PASSWORD (default Zaposleni123!)
//	BANK_CODE            (default 333)  — used to mint account numbers
//	BANK_BRANCH          (default 0001) — see pkg/account
//	BANK_CVV_PEPPER      (default "dev-pepper") — see pkg/cvv
//	SEED_FUNDS           (default 1; "0"/"false"/"off" opts out of the
//	                     investment-fund fixtures + demo mock data —
//	                     cypress.config.ts's resetBackend reseed sets
//	                     this so fund acceptance specs keep a pristine
//	                     klijent baseline)
//	SEED_ORDERS          (default 1; "0"/"false"/"off" opts out of the
//	                     historical done-order fixture that reconciles
//	                     "Pregled naloga" with the Profit Banke actuary
//	                     leaderboard — cypress.config.ts's resetBackend
//	                     reseed sets this so order specs self-fixture)
//
// The default password meets the spec's complexity rules (8–32 chars,
// ≥2 digits, ≥1 upper, ≥1 lower) but should be changed in any shared
// environment. Print the credentials on success so a fresh dev knows
// what to log in with.
package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/account"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/cvv"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/passwords"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	email := envOr("SEED_ADMIN_EMAIL", "admin@banka.local")
	username := envOr("SEED_ADMIN_USERNAME", "admin")
	password := envOr("SEED_ADMIN_PASSWORD", "Admin123!")

	if err := passwords.ValidateComplexity(password); err != nil {
		return fmt.Errorf("SEED_ADMIN_PASSWORD: %w", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	var adminID string
	const checkQ = `select id from "user".employees where 'admin' = any(permissions) limit 1`
	switch err := pool.QueryRow(ctx, checkQ).Scan(&adminID); err {
	case nil:
		fmt.Printf("seed: admin already exists (id=%s); skipping\n", adminID)
	default:
		// pgx.ErrNoRows is the expected "create me" path; any other error
		// is fatal. Comparing strings keeps this file dep-light.
		if err.Error() != "no rows in result set" {
			return fmt.Errorf("check existing admin: %w", err)
		}
		hash, err := passwords.Hash(password)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		const insertQ = `
        insert into "user".employees (
            email, username, password_hash,
            first_name, last_name, date_of_birth, gender, phone, address,
            position, department, active, permissions
        ) values (
            $1, $2, $3,
            'Admin', 'Banka 3', '1990-01-01', 'male', '+381000000000', 'Beograd',
            'Administrator', 'IT', true,
            array['admin','employee.read','employee.write','client.read','client.write','permission.grant']
        )
        returning id`
		if err := pool.QueryRow(ctx, insertQ, email, username, hash).Scan(&adminID); err != nil {
			return fmt.Errorf("insert admin: %w", err)
		}
		fmt.Printf("seed: admin created (id=%s)\n  email:    %s\n  username: %s\n  password: %s\n",
			adminID, email, username, password)
		fmt.Println("seed: change SEED_ADMIN_PASSWORD before any shared environment.")
	}

	if err := seedEmployee(ctx, pool); err != nil {
		return fmt.Errorf("seed employee: %w", err)
	}

	clientID, err := seedClient(ctx, pool,
		envOr("SEED_CLIENT_EMAIL", "klijent@banka.local"),
		envOr("SEED_CLIENT_PASSWORD", "Klijent123!"),
		"Test", "Klijent", "+381111000111")
	if err != nil {
		return fmt.Errorf("seed client: %w", err)
	}

	if err := seedBank(ctx, pool, clientID, adminID); err != nil {
		return fmt.Errorf("seed bank: %w", err)
	}

	if err := seedTrading(ctx, pool, clientID, adminID); err != nil {
		return fmt.Errorf("seed trading: %w", err)
	}

	// Second client — useful for testing flows that need two distinct
	// client logins (e.g. inter-client payments, OTC negotiation).
	// Idempotent on email like the first.
	client2ID, err := seedClient(ctx, pool,
		envOr("SEED_CLIENT2_EMAIL", "klijent2@banka.local"),
		envOr("SEED_CLIENT2_PASSWORD", "Klijent123!"),
		"Drugi", "Klijent", "+381111000222")
	if err != nil {
		return fmt.Errorf("seed second client: %w", err)
	}

	if err := seedOTC(ctx, pool, clientID, client2ID, adminID); err != nil {
		return fmt.Errorf("seed otc: %w", err)
	}

	// Investment funds + their demo mock data (invests, holdings,
	// performance history) mutate the klijent baseline — they debit
	// klijent's RSD account and pre-create klijent fund positions. The
	// c4 cypress acceptance specs assume a pristine klijent and
	// self-fixture their own funds, so cypress.config.ts's resetBackend
	// reseed sets SEED_FUNDS=0 to opt out. Default is on for `make seed`
	// (manual demo / cold boot).
	if isEnvTruthy(envOr("SEED_FUNDS", "1")) {
		if err := seedFunds(ctx, pool, clientID, adminID); err != nil {
			return fmt.Errorf("seed funds: %w", err)
		}
	} else {
		fmt.Println("seed: SEED_FUNDS disabled; skipping investment funds")
	}

	// Third client — non-trading. Drives the banking-trading-gate
	// cypress spec which asserts a client without `trading.client`
	// sees no Portfolio / Trgovina / OTC / Fondovi nav or tile.
	// seedClient always promotes its return to RoleClientTrading
	// for c3+c4 dev ergonomics, so we strip the perm right after via
	// direct SQL. Per spec p.4 client OTC + funds access is bundled
	// into trading.client, so stripping that single perm is enough.
	nonTradingID, err := seedClient(ctx, pool,
		envOr("SEED_CLIENT3_EMAIL", "klijent3@banka.local"),
		envOr("SEED_CLIENT3_PASSWORD", "Klijent123!"),
		"Treci", "Klijent", "+381111000333")
	if err != nil {
		return fmt.Errorf("seed third client: %w", err)
	}
	if _, err := pool.Exec(ctx, `
        update "user".clients
        set permissions = array(select unnest(permissions) except select 'trading.client')
        where id = $1`, nonTradingID); err != nil {
		return fmt.Errorf("strip trading.client from third client: %w", err)
	}
	return nil
}

// seedEmployee plants a single fully-activated regular employee with
// the agent role bundle (day-to-day banking ops, no admin or employee
// management). Idempotent on email. Useful for manual testing of
// non-admin flows without having to create one through the portal.
func seedEmployee(ctx context.Context, pool *pgxpool.Pool) error {
	email := envOr("SEED_EMPLOYEE_EMAIL", "zaposleni@banka.local")
	username := envOr("SEED_EMPLOYEE_USERNAME", "zaposleni")
	password := envOr("SEED_EMPLOYEE_PASSWORD", "Zaposleni123!")
	if err := passwords.ValidateComplexity(password); err != nil {
		return fmt.Errorf("SEED_EMPLOYEE_PASSWORD: %w", err)
	}
	var existing string
	switch err := pool.QueryRow(ctx,
		`select id from "user".employees where lower(email) = lower($1)`, email).Scan(&existing); err {
	case nil:
		fmt.Printf("seed: employee already exists (id=%s); skipping\n", existing)
		return nil
	default:
		if err.Error() != "no rows in result set" {
			return fmt.Errorf("check existing employee: %w", err)
		}
	}
	hash, err := passwords.Hash(password)
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}
	const q = `
        insert into "user".employees (
            email, username, password_hash,
            first_name, last_name, date_of_birth, gender, phone, address,
            position, department, active, permissions
        ) values (
            $1, $2, $3,
            'Petar', 'Petrović', '1992-06-15', 'male', '+381112233445', 'Beograd',
            'Agent', 'Šalter', true,
            $4
        ) returning id`
	var id string
	if err := pool.QueryRow(ctx, q, email, username, hash, permissions.RoleEmployeeAgent).Scan(&id); err != nil {
		return fmt.Errorf("insert employee: %w", err)
	}
	fmt.Printf("seed: employee created (id=%s)\n  email:    %s\n  username: %s\n  password: %s\n",
		id, email, username, password)
	return nil
}

// seedClient plants a single fully-activated client. Idempotent on
// email. Returns the (possibly-existing) client UUID so callers can
// hang bank fixtures off it.
func seedClient(ctx context.Context, pool *pgxpool.Pool, email, password, firstName, lastName, phone string) (string, error) {
	if err := passwords.ValidateComplexity(password); err != nil {
		return "", fmt.Errorf("password for %s: %w", email, err)
	}
	var existing string
	switch err := pool.QueryRow(ctx,
		`select id from "user".clients where lower(email) = lower($1)`, email).Scan(&existing); err {
	case nil:
		// Existing rows from before c3/c4 may be missing trading.client.
		// Append the full RoleClientTrading bundle idempotently and
		// strip the four deprecated client-side perms (collapsed into
		// trading.client per spec p.4 — see pkg/permissions/permissions.go)
		// so dev DBs self-heal across the refactor.
		if _, err := pool.Exec(ctx, `
            update "user".clients
            set permissions = (
                select array_agg(distinct p)
                from unnest(permissions || $2::text[]) as p
                where p not in ('otc.read','otc.trade.client','funds.read.client','funds.invest.client')
            )
            where id = $1`, existing, []string(permissions.RoleClientTrading)); err != nil {
			return "", fmt.Errorf("augment client perms: %w", err)
		}
		fmt.Printf("seed: client already exists (id=%s, email=%s); ensured trading perms\n", existing, email)
		return existing, nil
	default:
		if err.Error() != "no rows in result set" {
			return "", fmt.Errorf("check existing client: %w", err)
		}
	}
	hash, err := passwords.Hash(password)
	if err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	const q = `
        insert into "user".clients (
            email, password_hash,
            first_name, last_name, date_of_birth, gender, phone, address,
            active, permissions
        ) values (
            $1, $2,
            $3, $4, '1990-01-01', 'male', $5, 'Beograd',
            true, $6
        ) returning id`
	var id string
	if err := pool.QueryRow(ctx, q, email, hash, firstName, lastName, phone, []string(permissions.RoleClientTrading)).Scan(&id); err != nil {
		return "", fmt.Errorf("insert client: %w", err)
	}
	fmt.Printf("seed: client created (id=%s)\n  email:    %s\n  password: %s\n", id, email, password)
	return id, nil
}

// seedBank plants a small but representative bank-schema dataset for
// the seeded client: two companies, three accounts (RSD personal +
// EUR personal + RSD business), two cards (active on the RSD account,
// blocked on the EUR one), two loans (approved + paid-off), and two
// loan requests (pending + rejected) so each portal/banking section
// has data on first boot. Idempotent: if the client already has any
// account, the function no-ops.
//
// We write directly to the bank schema (same convention as seedClient
// vs the user schema) instead of going through the bank service —
// cuts the seed dependency surface to plain pgx, and the bank service
// can be down when seed runs (we do this after `make migrate` but
// before the service is necessarily up).
func seedBank(ctx context.Context, pool *pgxpool.Pool, clientID, adminID string) error {
	if clientID == "" || adminID == "" {
		return fmt.Errorf("seedBank: clientID and adminID are required")
	}
	// Idempotency. Three branches because cypress's resetBackend can
	// truncate just the user schema (re-mints clientID) while bank rows
	// stay around — leaving orphan fixtures pointing at a now-deleted
	// client UUID. We don't want the second seed run to either crash on
	// the registry_id unique constraint or quietly leave dangling
	// references that show up in the portal as ownerless accounts.
	//
	//   a) accounts owned by this client → fixtures already planted
	//      against this client; skip.
	//   b) any row in bank.companies with the fixture registry_ids →
	//      fixtures planted against a previous (now-deleted) client;
	//      repoint to the current clientID/adminID instead of skipping.
	//   c) neither → no fixtures; insert.
	var have int
	if err := pool.QueryRow(ctx,
		`select count(*) from "bank".accounts where owner_client_id=$1`, clientID).Scan(&have); err != nil {
		return fmt.Errorf("count accounts: %w", err)
	}
	if have > 0 {
		fmt.Println("seed: bank fixtures already present for client; skipping")
		return nil
	}
	var oldClientID string
	switch err := pool.QueryRow(ctx,
		`select owner_client_id from "bank".companies
		 where registry_id in ('12345678','23456789')
		 limit 1`).Scan(&oldClientID); err {
	case nil:
		// Orphaned fixtures present — repoint everything keyed on the
		// stale client UUID to the new clientID/adminID. Single tx so
		// either every reference is fixed or none.
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin repoint tx: %w", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		stmts := []struct {
			q    string
			args []any
		}{
			{`update "bank".companies set owner_client_id=$1 where owner_client_id=$2`, []any{clientID, oldClientID}},
			{`update "bank".accounts set owner_client_id=$1, created_by_employee_id=$3 where owner_client_id=$2`, []any{clientID, oldClientID, adminID}},
			{`update "bank".loans set client_id=$1 where client_id=$2`, []any{clientID, oldClientID}},
			{`update "bank".loan_requests set client_id=$1, decided_by_employee_id=case when decided_by_employee_id is not null then $3::uuid else null end where client_id=$2`, []any{clientID, oldClientID, adminID}},
			{`update "bank".payment_recipients set client_id=$1 where client_id=$2`, []any{clientID, oldClientID}},
		}
		for _, s := range stmts {
			if _, err := tx.Exec(ctx, s.q, s.args...); err != nil {
				return fmt.Errorf("repoint orphan fixtures: %w", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit repoint: %w", err)
		}
		fmt.Printf("seed: bank fixtures repointed from old client %s to %s\n", oldClientID, clientID)
		return nil
	default:
		if err.Error() != "no rows in result set" {
			return fmt.Errorf("check fixture companies: %w", err)
		}
	}

	bankCode := envOr("BANK_CODE", "333")
	branch := envOr("BANK_BRANCH", "0001")
	pepper := envOr("BANK_CVV_PEPPER", "dev-pepper")

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1) Two companies owned by the client. Two so the portal "Firme"
	// page shows a list, not a single row, and so the new-account
	// flow has more than one option in the company selector.
	var companyID, secondCompanyID string
	if err := tx.QueryRow(ctx, `
        insert into "bank".companies
            (name, registry_id, tax_id, activity_code, address, owner_client_id)
        values ($1,$2,$3,$4,$5,$6)
        returning id`,
		"Klikovac DOO", "12345678", "987654321", "62.01", "Beograd", clientID,
	).Scan(&companyID); err != nil {
		return fmt.Errorf("insert company: %w", err)
	}
	if err := tx.QueryRow(ctx, `
        insert into "bank".companies
            (name, registry_id, tax_id, activity_code, address, owner_client_id)
        values ($1,$2,$3,$4,$5,$6)
        returning id`,
		"TechStart DOO", "23456789", "876543210", "62.09", "Novi Sad", clientID,
	).Scan(&secondCompanyID); err != nil {
		return fmt.Errorf("insert second company: %w", err)
	}

	// 2) Personal RSD checking + personal EUR + business RSD account.
	rsdNumber, err := account.Generate(bankCode, branch, account.TypePersonalChecking)
	if err != nil {
		return fmt.Errorf("rsd account number: %w", err)
	}
	eurNumber, err := account.Generate(bankCode, branch, account.TypePersonalFX)
	if err != nil {
		return fmt.Errorf("eur account number: %w", err)
	}
	bizNumber, err := account.Generate(bankCode, branch, account.TypeBusinessChecking)
	if err != nil {
		return fmt.Errorf("biz account number: %w", err)
	}

	// Spec p.12: standard RSD checking carries 255 RSD/month; default
	// limits 120k daily / 1M monthly. EUR account skips the maintenance
	// fee column (covered separately for FX). We keep the numbers
	// boring — these are devmode fixtures, not test vectors.
	insertAccount := func(number, name, kind, subtype, currency string,
		companyID *string, balance string, dailyLimit, monthlyLimit string,
		maintenanceFee string,
	) (string, error) {
		var id string
		err := tx.QueryRow(ctx, `
            insert into "bank".accounts
                (number, name, owner_client_id, company_id, created_by_employee_id,
                 kind, subtype, currency, status,
                 balance, available_balance, maintenance_fee,
                 daily_limit, monthly_limit)
            values ($1,$2,$3,$4,$5,$6,$7,$8,'active',$9,$9,$10,$11,$12)
            returning id`,
			number, name, clientID, companyID, adminID,
			kind, subtype, currency,
			balance, maintenanceFee, dailyLimit, monthlyLimit,
		).Scan(&id)
		return id, err
	}

	rsdID, err := insertAccount(rsdNumber, "Tekući RSD", "personal_checking_rsd", "standard", "RSD",
		nil, "250000", "120000", "1000000", "255")
	if err != nil {
		return fmt.Errorf("insert rsd account: %w", err)
	}
	eurID, err := insertAccount(eurNumber, "Devizni EUR", "personal_fx", "unspecified", "EUR",
		nil, "1500", "1000", "5000", "0")
	if err != nil {
		return fmt.Errorf("insert eur account: %w", err)
	}
	if _, err := insertAccount(bizNumber, "Poslovni RSD", "business_checking_rsd", "doo", "RSD",
		&companyID, "500000", "300000", "3000000", "1000"); err != nil {
		return fmt.Errorf("insert business account: %w", err)
	}

	// 3) Two cards: active Visa on the RSD account and a blocked
	// Mastercard on the EUR account, so the kartice list has more
	// than one row and a non-active example. CVV 123 for both (we
	// never read it back; the field is just non-null per schema).
	cvvHash, err := cvv.Hash("123", pepper)
	if err != nil {
		return fmt.Errorf("hash cvv: %w", err)
	}
	const visaPAN = "4111111111111111"
	const mastercardPAN = "5500000000000004"
	if _, err := tx.Exec(ctx, `
        insert into "bank".cards
            (number, cvv_hash, brand, name, account_id, card_limit, expires_at, status)
        values
            ($1,$2,'visa','Debit',$3,$5, now() + interval '4 years','active'),
            ($4,$2,'mastercard','Debit',$6,$7, now() + interval '4 years','blocked')`,
		visaPAN, cvvHash, rsdID, mastercardPAN, "100000", eurID, "5000",
	); err != nil {
		return fmt.Errorf("insert cards: %w", err)
	}

	// 4) Approved cash loan: 100k RSD / 12 installments / fixed 9.5%.
	// Numbers are illustrative; the c3 spec is what matters for the
	// real loan flow. First installment is "paid" so the loan UI
	// shows progress without us having to run the cron.
	const principal = "100000"
	const installmentAmount = "8788.49" // ~ pmt(0.095/12, 12, -100000)
	const monthlyInterest = "9.5000"
	const baseRate = "8.0000"
	const margin = "1.5000"
	var loanID string
	if err := tx.QueryRow(ctx, `
        insert into "bank".loans
            (loan_number, client_id, account_id, loan_type, interest_type,
             principal, currency, base_rate, margin, current_offset,
             installments_total, installment_amount, remaining_principal,
             next_installment_date, next_installment_amount, status,
             contracted_at, matures_at)
        values
            ('LN-DEV-0001', $1, $2, 'cash', 'fixed',
             $3, 'RSD', $4, $5, 0,
             12, $6, $3,
             current_date + interval '30 days', $6, 'approved',
             now(), current_date + interval '12 months')
        returning id`,
		clientID, rsdID, principal, baseRate, margin, installmentAmount,
	).Scan(&loanID); err != nil {
		return fmt.Errorf("insert loan: %w", err)
	}
	// One paid + one upcoming installment.
	if _, err := tx.Exec(ctx, `
        insert into "bank".loan_installments
            (loan_id, sequence_number, amount, interest_rate_at_due, currency,
             expected_due_date, actual_paid_at, status)
        values
            ($1, 1, $2, $3, 'RSD', current_date - interval '1 day', now(), 'paid'),
            ($1, 2, $2, $3, 'RSD', current_date + interval '30 days', null, 'unpaid')
        `, loanID, installmentAmount, monthlyInterest); err != nil {
		return fmt.Errorf("insert installments: %w", err)
	}

	// 5) A paid-off auto loan, so the krediti list shows non-active
	// history and the portal "Krediti" page has a paid_off example.
	const paidOffPrincipal = "60000"
	const paidOffInstallment = "5300.00"
	if _, err := tx.Exec(ctx, `
        insert into "bank".loans
            (loan_number, client_id, account_id, loan_type, interest_type,
             principal, currency, base_rate, margin, current_offset,
             installments_total, installment_amount, remaining_principal,
             next_installment_date, next_installment_amount, status,
             contracted_at, matures_at)
        values
            ('LN-DEV-0002', $1, $2, 'auto', 'fixed',
             $3, 'RSD', '7.5000', '1.0000', 0,
             12, $4, 0,
             null, null, 'paid_off',
             now() - interval '13 months', current_date - interval '1 month')`,
		clientID, rsdID, paidOffPrincipal, paidOffInstallment,
	); err != nil {
		return fmt.Errorf("insert paid-off loan: %w", err)
	}

	// 6) Two loan requests so the portal "Zahtevi za kredit" page is
	// not empty: one pending (the agent should approve/reject it),
	// one already rejected (so the history is non-empty too). The
	// pending request is keyed against the RSD account; the rejected
	// one against the business account.
	if _, err := tx.Exec(ctx, `
        insert into "bank".loan_requests
            (client_id, account_id, loan_type, interest_type,
             amount, currency, purpose, monthly_salary,
             employment_status, employment_duration_months,
             installments_total, contact_phone, status,
             rejection_reason, decided_at, decided_by_employee_id)
        values
            ($1, $2, 'cash', 'fixed',
             150000, 'RSD', 'Renoviranje stana', 90000,
             'permanent', 36,
             24, '+381111000111', 'pending',
             null, null, null),
            ($1, $3, 'housing', 'variable',
             5000000, 'RSD', 'Kupovina stana', 90000,
             'permanent', 36,
             120, '+381111000111', 'rejected',
             'Iznos prekoračuje limit za stambene kredite na poslovnom računu',
             now() - interval '7 days', $4)`,
		clientID, rsdID, bizID(tx, ctx, clientID), adminID,
	); err != nil {
		return fmt.Errorf("insert loan requests: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Printf("seed: bank fixtures created\n"+
		"  companies:       %s (Klikovac DOO), %s (TechStart DOO)\n"+
		"  rsd account:     %s (id=%s)\n  eur account:     %s (id=%s)\n"+
		"  business acct:   %s\n"+
		"  cards:           %s (Visa, active), %s (Mastercard, blocked) — CVV=123\n"+
		"  loans:           LN-DEV-0001 (active, %s RSD, 12 rata), LN-DEV-0002 (paid_off)\n"+
		"  loan requests:   1 pending (cash 150000 RSD), 1 rejected (housing 5000000 RSD)\n",
		companyID, secondCompanyID, rsdNumber, rsdID, eurNumber, eurID,
		bizNumber, visaPAN, mastercardPAN, principal)
	return nil
}

// seedTrading plants the c3 fixture surface so the trading portal has
// data on first boot:
//
//  1. A USD personal_fx trading account for the seeded client (300k
//     USD opening balance), so they can place orders on USD-listed
//     stocks without first running an FX deposit.
//  2. The seeded employee (zaposleni@banka.local) gets promoted to
//     actuary agent: permissions augmented with `actuary` +
//     `actuary.agent`, an `actuary_info` row planted with daily_limit
//     = 200000 RSD and need_approval=false. The bootstrap admin gets
//     no actuary_info row; admin is implicitly a supervisor per
//     `requireSupervisor`.
//  3. Three exchanges: NYSE (XNYS / USD), London (XLON / GBP), and
//     Borsa Beograd (XBEL / RSD). Hours and IANA timezones are
//     realistic so the after-hours math behaves under both Belgrade
//     and US wall-clocks.
//  4. Five stocks (AAPL/MSFT/GOOGL on NYSE, VOD on XLON, NIS on XBEL),
//     one future (CL crude oil), one forex pair (EUR/USD), one option
//     (AAPL call expiring +60 days). Listings for everything except
//     the option — options read their premium directly off the
//     security row.
//
// Idempotent: if any exchange row already exists, we no-op.
//
// Same direct-pgx convention as seedBank: cuts deps and lets the seed
// run before the trading service is necessarily up.
func seedTrading(ctx context.Context, pool *pgxpool.Pool, clientID, adminID string) error {
	if clientID == "" || adminID == "" {
		return fmt.Errorf("seedTrading: clientID and adminID are required")
	}

	bankCode := envOr("BANK_CODE", "333")
	branch := envOr("BANK_BRANCH", "0001")

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1) USD trading account for the client (alongside the EUR one
	// seeded by seedBank). 300k USD opening balance — enough for the
	// MSFT-style price-points the smoke tests use without slipping into
	// margin paths. Idempotent: if the client already has a USD
	// personal_fx account we leave it alone (could be from a prior seed
	// or hand-rolled).
	var usdAccountID, usdNumber string
	switch err := tx.QueryRow(ctx, `
        select id, number from "bank".accounts
        where owner_client_id = $1 and currency = 'USD' and kind = 'personal_fx'
        limit 1`, clientID).Scan(&usdAccountID, &usdNumber); err {
	case nil:
		// already there
	default:
		if err.Error() != "no rows in result set" {
			return fmt.Errorf("check existing usd account: %w", err)
		}
		usdNumber, err = account.Generate(bankCode, branch, account.TypePersonalFX)
		if err != nil {
			return fmt.Errorf("usd account number: %w", err)
		}
		if err := tx.QueryRow(ctx, `
            insert into "bank".accounts
                (number, name, owner_client_id, created_by_employee_id,
                 kind, subtype, currency, status,
                 balance, available_balance, maintenance_fee,
                 daily_limit, monthly_limit)
            values ($1, $2, $3, $4,
                    'personal_fx', 'unspecified', 'USD', 'active',
                    $5, $5, '0',
                    '0', '0')
            returning id`,
			usdNumber, "Trgovinski USD", clientID, adminID, "300000",
		).Scan(&usdAccountID); err != nil {
			return fmt.Errorf("insert usd trading account: %w", err)
		}
	}

	// 2) Two dedicated trading-side employees, separate from the
	// banking employee zaposleni: an actuary agent (subject to
	// daily_limit) and an actuary supervisor (no limit, can approve
	// agent orders). Keeping zaposleni unchanged means each of the
	// three employee profiles in the spec — banking-only, actuary
	// agent, actuary supervisor — has a distinct fixture.
	agentPerms := dedupePerms(append(append([]string{},
		permissions.RoleEmployeeAgent...),
		permissions.RoleEmployeeActuaryAgent...))
	supervisorPerms := dedupePerms(append(append([]string{},
		permissions.RoleEmployeeAgent...),
		permissions.RoleEmployeeActuarySupervisor...))

	insertActuary := func(email, username, password, firstName, lastName, phone, position string, perms []string) (string, error) {
		if err := passwords.ValidateComplexity(password); err != nil {
			return "", fmt.Errorf("password for %s: %w", email, err)
		}
		var existing string
		switch err := tx.QueryRow(ctx,
			`select id from "user".employees where lower(email) = lower($1)`, email,
		).Scan(&existing); err {
		case nil:
			// Self-heal, mirroring seedClient's "ensured trading perms"
			// path: an actuary row from an earlier seed — or one a
			// cypress spec demoted (S46 strips funds.manage.supervisor)
			// — keeps its id but must still carry its full role bundle,
			// otherwise e.g. the supervisor can't open Profit Banke
			// (bank.profit.read lives in RoleEmployeeActuarySupervisor).
			// Idempotent union. Under cypress this branch is never hit
			// (resetBackend truncates "user".employees first), so it's
			// purely a dev-DB ergonomic and changes no spec baseline.
			if _, err := tx.Exec(ctx, `
                update "user".employees
                set permissions = (
                    select array_agg(distinct p)
                    from unnest(permissions || $2::text[]) as p
                )
                where id = $1`, existing, perms); err != nil {
				return "", fmt.Errorf("ensure %s perms: %w", email, err)
			}
			fmt.Printf("seed: %s already exists (id=%s); ensured role perms\n", position, existing)
			return existing, nil
		default:
			if err.Error() != "no rows in result set" {
				return "", fmt.Errorf("check existing %s: %w", email, err)
			}
		}
		hash, err := passwords.Hash(password)
		if err != nil {
			return "", fmt.Errorf("hash %s: %w", email, err)
		}
		var id string
		if err := tx.QueryRow(ctx, `
            insert into "user".employees (
                email, username, password_hash,
                first_name, last_name, date_of_birth, gender, phone, address,
                position, department, active, permissions
            ) values (
                $1, $2, $3,
                $4, $5, '1990-01-01', 'male', $6, 'Beograd',
                $7, 'Trgovina', true, $8
            ) returning id`,
			email, username, hash,
			firstName, lastName, phone, position, perms,
		).Scan(&id); err != nil {
			return "", fmt.Errorf("insert %s: %w", email, err)
		}
		fmt.Printf("seed: %s created (id=%s)\n  email:    %s\n  username: %s\n  password: %s\n",
			position, id, email, username, password)
		return id, nil
	}

	aktuarID, err := insertActuary(
		envOr("SEED_ACTUARY_EMAIL", "aktuar@banka.local"),
		envOr("SEED_ACTUARY_USERNAME", "aktuar"),
		envOr("SEED_ACTUARY_PASSWORD", "Aktuar123!"),
		"Marko", "Marković", "+381112233556", "Aktuar agent", agentPerms,
	)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
        insert into "trading".actuary_info
            (employee_id, type, daily_limit, used_limit, need_approval)
        values ($1, 'agent', 200000, 0, false)
        on conflict (employee_id) do nothing`, aktuarID); err != nil {
		return fmt.Errorf("insert aktuar actuary_info: %w", err)
	}

	supervisorID, err := insertActuary(
		envOr("SEED_SUPERVISOR_EMAIL", "supervizor@banka.local"),
		envOr("SEED_SUPERVISOR_USERNAME", "supervizor"),
		envOr("SEED_SUPERVISOR_PASSWORD", "Supervizor123!"),
		"Jovana", "Jovanović", "+381112233557", "Aktuar supervizor", supervisorPerms,
	)
	if err != nil {
		return err
	}
	// Spec p.38: supervisors don't have a personal limit. The trading
	// service's upsert path forces 0/false for type='supervisor'; we
	// match that here so the seeded row is consistent with what the
	// service would write itself.
	if _, err := tx.Exec(ctx, `
        insert into "trading".actuary_info
            (employee_id, type, daily_limit, used_limit, need_approval)
        values ($1, 'supervisor', 0, 0, false)
        on conflict (employee_id) do nothing`, supervisorID); err != nil {
		return fmt.Errorf("insert supervisor actuary_info: %w", err)
	}

	// 3) Exchanges. Hours are local wall-clock per spec p.39; the
	// service's after-hours window applies the timezone shift on the
	// fly.
	type exch struct {
		mic, name, acronym, polity, currency, timezone string
		open, close                                    string
	}
	exchanges := []exch{
		{"XNYS", "New York Stock Exchange", "NYSE", "USA", "USD", "America/New_York", "09:30", "16:00"},
		{"XLON", "London Stock Exchange", "LSE", "United Kingdom", "GBP", "Europe/London", "08:00", "16:30"},
		{"XBEL", "Beogradska berza", "BELEX", "Srbija", "RSD", "Europe/Belgrade", "09:30", "14:00"},
	}
	// Seed exchanges with override_state='open' so the dev stack accepts
	// trades at any wall-clock — this is a simulation, not a real market.
	// Admins can still flip the override per exchange via the /portal/berze
	// admin UI (or `PATCH /api/v1/exchanges/{mic}/override`) to exercise
	// the spec p.39 closed-market path; re-running `make seed` forces them
	// back to open via on-conflict update.
	for _, e := range exchanges {
		if _, err := tx.Exec(ctx, `
            insert into "trading".exchanges
                (mic, name, acronym, polity, currency, timezone, open_local, close_local, override_state)
            values ($1, $2, $3, $4, $5, $6, $7::time, $8::time, 'open')
            on conflict (mic) do update set override_state='open'`,
			e.mic, e.name, e.acronym, e.polity, e.currency, e.timezone, e.open, e.close,
		); err != nil {
			return fmt.Errorf("insert exchange %s: %w", e.mic, err)
		}
	}

	// 4) Securities + listings. Helper closures keep the boilerplate
	// short: insertStock returns the new uuid so the option chain can
	// reference AAPL.
	insertStock := func(ticker, name, mic, currency string, outstanding int64, dividend string, listing struct {
		price, ask, bid string
		volume          int64
	}) (string, error) {
		// ON CONFLICT DO UPDATE SET <noop> so RETURNING fires whether we
		// inserted or matched an existing (ticker, type) row. Cheap and
		// keeps the seed re-runnable on partial state.
		var id string
		if err := tx.QueryRow(ctx, `
            insert into "trading".securities
                (ticker, name, type, exchange_mic, currency, outstanding_shares, dividend_yield)
            values ($1, $2, 'stock', $3, $4, $5, $6::numeric)
            on conflict (ticker, type) do update set ticker = excluded.ticker
            returning id`,
			ticker, name, mic, currency, outstanding, dividend,
		).Scan(&id); err != nil {
			return "", fmt.Errorf("insert stock %s: %w", ticker, err)
		}
		if _, err := tx.Exec(ctx, `
            insert into "trading".listings
                (security_id, exchange_mic, price, ask, bid, volume, change_amt, contract_size)
            values ($1, $2, $3::numeric, $4::numeric, $5::numeric, $6, 0, 1)
            on conflict (security_id) do nothing`,
			id, mic, listing.price, listing.ask, listing.bid, listing.volume,
		); err != nil {
			return "", fmt.Errorf("insert listing %s: %w", ticker, err)
		}
		return id, nil
	}
	type quote struct {
		price, ask, bid string
		volume          int64
	}

	aaplID, err := insertStock("AAPL", "Apple Inc.", "XNYS", "USD", 15500000000, "0.005",
		quote{"190.50", "190.55", "190.45", 50000000})
	if err != nil {
		return err
	}
	if _, err := insertStock("MSFT", "Microsoft Corporation", "XNYS", "USD", 7430000000, "0.0072",
		quote{"450.10", "450.20", "450.00", 25000000}); err != nil {
		return err
	}
	if _, err := insertStock("GOOGL", "Alphabet Inc.", "XNYS", "USD", 12500000000, "0",
		quote{"175.30", "175.40", "175.20", 18000000}); err != nil {
		return err
	}
	if _, err := insertStock("VOD", "Vodafone Group plc", "XLON", "GBP", 25700000000, "0.097",
		quote{"68.40", "68.50", "68.30", 32000000}); err != nil {
		return err
	}
	nisID, err := insertStock("NIS", "Naftna industrija Srbije", "XBEL", "RSD", 163000000, "0.06",
		quote{"850.00", "853.00", "847.00", 200000})
	if err != nil {
		return err
	}
	// Extra USD stocks that exist purely to give the OTC discovery
	// board ("Dostupne hartije") more inventory — klijent2 holds and
	// publishes these in seedOTC so klijent sees a fuller board.
	if _, err := insertStock("AMZN", "Amazon.com, Inc.", "XNYS", "USD", 10300000000, "0",
		quote{"185.20", "185.30", "185.10", 22000000}); err != nil {
		return err
	}
	if _, err := insertStock("TSLA", "Tesla, Inc.", "XNYS", "USD", 3180000000, "0",
		quote{"242.75", "242.90", "242.60", 41000000}); err != nil {
		return err
	}

	// seedSyntheticHistory plants ~60 business days of random-walk price
	// history into trading.listing_daily_price_info. Used for:
	//   - NIS (Alpha Vantage doesn't cover BELEX)
	//   - CL  (futures, seed-only per spec p.41)
	//   - EURUSD (AV ships only spot for forex via the wired
	//     CURRENCY_EXCHANGE_RATE endpoint; FX_DAILY isn't wired)
	// The walk is deterministic in the listing UUID — re-runs of the
	// seed (or fresh DBs that happen to mint the same UUIDs) produce
	// the same chart shape. ON CONFLICT (listing_id, date) DO NOTHING
	// leaves any row already written by today's AV refresh untouched.
	// AV-covered stocks (AAPL/MSFT/GOOGL/VOD) are intentionally skipped:
	// their daily refresh accumulates real history naturally and mixing
	// real today + synthetic past would lie about the chart.
	seedSyntheticHistory := func(securityID string, days int) error {
		var (
			listingID string
			priceStr  string
			volume    int64
		)
		if err := tx.QueryRow(ctx, `
            select id, price::text, volume from "trading".listings where security_id = $1
        `, securityID).Scan(&listingID, &priceStr, &volume); err != nil {
			return fmt.Errorf("lookup listing for synthetic history (%s): %w", securityID, err)
		}
		basePrice, err := strconv.ParseFloat(priceStr, 64)
		if err != nil {
			return fmt.Errorf("parse seed price %q: %w", priceStr, err)
		}

		h := fnv.New64a()
		_, _ = h.Write([]byte(listingID))
		seed := h.Sum64()
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))

		// Build the price series chronologically: prices[days] is today
		// (the listing's current price); each earlier index is derived
		// from the next by an inverse multiplicative step so the walk
		// terminates exactly on the seed price.
		prices := make([]float64, days+1)
		prices[days] = basePrice
		for i := days - 1; i >= 0; i-- {
			step := 1.0 + (rng.Float64()*0.04 - 0.02) // ±2% daily
			prices[i] = prices[i+1] / step
		}

		loc, locErr := time.LoadLocation("Europe/Belgrade")
		if locErr != nil {
			loc = time.UTC
		}
		now := time.Now().In(loc)
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

		// Skip i=days (today) — owned by the live listing row + today's
		// refresh. Walk i=0..days-1 = days-back to 1-day-back.
		for i := 0; i < days; i++ {
			d := today.AddDate(0, 0, -(days - i))
			if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
				continue
			}
			p := prices[i]
			prev := p
			if i > 0 {
				prev = prices[i-1]
			}
			change := p - prev
			bid := p * 0.999
			ask := p * 1.001
			vol := int64(float64(volume) * (0.5 + rng.Float64()))
			if _, err := tx.Exec(ctx, `
                insert into "trading".listing_daily_price_info
                    (listing_id, date, price, ask, bid, change_amt, volume)
                values ($1, $2, $3::numeric, $4::numeric, $5::numeric, $6::numeric, $7)
                on conflict (listing_id, date) do nothing`,
				listingID, d,
				fmt.Sprintf("%.4f", p),
				fmt.Sprintf("%.4f", ask),
				fmt.Sprintf("%.4f", bid),
				fmt.Sprintf("%.4f", change),
				vol,
			); err != nil {
				return fmt.Errorf("insert daily history (%s): %w", listingID, err)
			}
		}
		return nil
	}

	if err := seedSyntheticHistory(nisID, 60); err != nil {
		return err
	}

	// Future: WTI crude oil, NYMEX-listed (we use XNYS for the seed
	// since we don't have a separate futures exchange row, and the
	// service treats exchange as an organizational hint, not a check).
	var futureID string
	if err := tx.QueryRow(ctx, `
        insert into "trading".securities
            (ticker, name, type, exchange_mic, currency,
             contract_size, contract_unit, settlement_date)
        values ('CL', 'Crude Oil WTI', 'future', 'XNYS', 'USD',
                1000, 'Barrel', current_date + interval '90 days')
        on conflict (ticker, type) do update set ticker = excluded.ticker
        returning id`).Scan(&futureID); err != nil {
		return fmt.Errorf("insert future: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        insert into "trading".listings
            (security_id, exchange_mic, price, ask, bid, volume, change_amt, contract_size)
        values ($1, 'XNYS', 78.50, 78.55, 78.45, 350000, 0, 1000)
        on conflict (security_id) do nothing`,
		futureID,
	); err != nil {
		return fmt.Errorf("insert future listing: %w", err)
	}
	if err := seedSyntheticHistory(futureID, 60); err != nil {
		return err
	}

	// Forex: EUR/USD. Listings on forex are optional per spec p.45-48
	// (Idea 1) but we plant one so the FE has a "live rate" row to
	// render without falling through to the exchange service.
	var forexID string
	if err := tx.QueryRow(ctx, `
        insert into "trading".securities
            (ticker, name, type, currency,
             base_currency, quote_currency, liquidity, contract_size)
        values ('EURUSD', 'Euro / US Dollar', 'forex', 'USD',
                'EUR', 'USD', 'high', 1000)
        on conflict (ticker, type) do update set ticker = excluded.ticker
        returning id`).Scan(&forexID); err != nil {
		return fmt.Errorf("insert forex: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        insert into "trading".listings
            (security_id, price, ask, bid, volume, change_amt, contract_size)
        values ($1, 1.0850, 1.0852, 1.0848, 0, 0, 1000)
        on conflict (security_id) do nothing`,
		forexID,
	); err != nil {
		return fmt.Errorf("insert forex listing: %w", err)
	}
	if err := seedSyntheticHistory(forexID, 60); err != nil {
		return err
	}

	// Options: a small AAPL chain with strikes 180 / 190 / 200 at the
	// same +60d expiry plus an OTM-call strike 200 at +120d so the
	// chain UI has more than one settlement date to switch between.
	// All read their `premium` straight off the security row (no
	// listings). Spot 190.50 ⇒ strike 180 CALL is ITM / PUT OOM,
	// strike 190 is ATM, strike 200 CALL is OOM / PUT ITM. The
	// strike-180 PUT (OOM) doubles as the fixture for the spec
	// p.61.d "out-of-the-money" exercise gate test.
	insertOption := func(ticker, name, optType, strike, vol, premium, expiryInterval string) (string, error) {
		var id string
		if err := tx.QueryRow(ctx, `
            insert into "trading".securities
                (ticker, name, type, exchange_mic, currency,
                 underlying_security_id, option_type, strike_price,
                 implied_volatility, premium, settlement_date,
                 contract_size)
            values ($1, $2, 'option', 'XNYS', 'USD',
                    $3, $4, $5::numeric,
                    $6::numeric, $7::numeric, current_date + $8::interval,
                    100)
            on conflict (ticker, type) do update set ticker = excluded.ticker
            returning id`,
			ticker, name, aaplID, optType, strike, vol, premium, expiryInterval,
		).Scan(&id); err != nil {
			return "", fmt.Errorf("insert option %s: %w", ticker, err)
		}
		return id, nil
	}

	optionID, err := insertOption("AAPL-C-190", "Apple Call 190 USD", "call", "190.00", "0.275", "8.50", "60 days")
	if err != nil {
		return err
	}
	if _, err := insertOption("AAPL-P-190", "Apple Put 190 USD", "put", "190.00", "0.275", "8.00", "60 days"); err != nil {
		return err
	}
	if _, err := insertOption("AAPL-C-180", "Apple Call 180 USD", "call", "180.00", "0.270", "12.00", "60 days"); err != nil {
		return err
	}
	oomPutID, err := insertOption("AAPL-P-180", "Apple Put 180 USD", "put", "180.00", "0.270", "2.00", "60 days")
	if err != nil {
		return err
	}
	if _, err := insertOption("AAPL-C-200", "Apple Call 200 USD", "call", "200.00", "0.280", "3.00", "60 days"); err != nil {
		return err
	}
	if _, err := insertOption("AAPL-P-200", "Apple Put 200 USD", "put", "200.00", "0.280", "11.00", "60 days"); err != nil {
		return err
	}
	if _, err := insertOption("AAPL-C-200-Q", "Apple Call 200 USD (Q)", "call", "200.00", "0.300", "5.50", "120 days"); err != nil {
		return err
	}

	// Plant an option holding for the actuary agent so the FE
	// option-exercise flow (spec p.61.d) has a fixture to drive
	// against without first running an OTC negotiation. Account
	// references the bank's USD forex_book account: actuary settles
	// must target a bank-owned account (KindSystem ∪ KindForexBook),
	// and the trade-settle layer rejects the menjačnica house itself
	// (KindSystem) on the actuary path because that collapses to a
	// no-op pair (spec p.56 + 2026-05-09 audit). The forex_book is
	// the bank's per-currency trading-book counterparty. AAPL spot
	// 190.50 > strike 190.00 ⇒ ITM, so the FE dialog renders "In
	// the money" and Potvrdi is clickable.
	var bankUSDAcctID string
	if err := tx.QueryRow(ctx, `
        select id from "bank".accounts
         where kind = 'forex_book'
           and currency = 'USD'
         limit 1`).Scan(&bankUSDAcctID); err != nil {
		return fmt.Errorf("lookup bank USD forex_book account: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        insert into "trading".portfolio_holdings
            (user_id, user_kind, security_id, account_id,
             quantity, weighted_avg_price)
        values ($1, 'employee', $2, $3, 5, 8.50)
        on conflict (user_id, security_id, account_id) do nothing`,
		aktuarID, optionID, bankUSDAcctID,
	); err != nil {
		return fmt.Errorf("insert agent option holding: %w", err)
	}
	// OOM PUT holding (strike 180, spot 190.50 ⇒ underlying > strike,
	// so the put is out of the money). Drives the spec p.61.d
	// "Potvrdi disabled when OOM" exercise-gate test.
	if _, err := tx.Exec(ctx, `
        insert into "trading".portfolio_holdings
            (user_id, user_kind, security_id, account_id,
             quantity, weighted_avg_price)
        values ($1, 'employee', $2, $3, 2, 2.00)
        on conflict (user_id, security_id, account_id) do nothing`,
		aktuarID, oomPutID, bankUSDAcctID,
	); err != nil {
		return fmt.Errorf("insert agent OOM put holding: %w", err)
	}

	// Client portfolio: 10 AAPL @ $170 cost basis (current $190.50 ⇒
	// ~$200 market profit) + 2 CL future @ $70 cost basis (current
	// $78.50). Account references the client's seeded USD trading
	// account. Drives the /banking/portfolio + sell-deeplink tests
	// without needing the execution worker to fill an order first.
	var clFutID string
	if err := tx.QueryRow(ctx,
		`select id from "trading".securities where ticker='CL' and type='future' limit 1`,
	).Scan(&clFutID); err != nil {
		return fmt.Errorf("lookup CL future id: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        insert into "trading".portfolio_holdings
            (user_id, user_kind, security_id, account_id,
             quantity, weighted_avg_price)
        values
            ($1, 'client', $2, $3, 10, 170.00),
            ($1, 'client', $4, $3, 2, 70.00)
        on conflict (user_id, security_id, account_id) do nothing`,
		clientID, aaplID, usdAccountID, clFutID,
	); err != nil {
		return fmt.Errorf("insert client holdings: %w", err)
	}

	// Realized-gain ledger entries: one positive ($99.50 ≈ ~10000 RSD,
	// taxed=false ⇒ unpaid 15% ≈ 1500 RSD) and one loss row (-$40,
	// taxed=false ⇒ doesn't add tax). Drives the /portal/porez board
	// with a non-zero "Neplaćeno" column and a loss-row in the per-
	// user detail. Skipped if any realized_gains row already exists
	// for this client (idempotent re-runs).
	var rgCount int
	if err := tx.QueryRow(ctx,
		`select count(*) from "trading".realized_gains where user_id = $1`, clientID,
	).Scan(&rgCount); err != nil {
		return fmt.Errorf("count realized_gains: %w", err)
	}
	if rgCount == 0 {
		var msftID string
		if err := tx.QueryRow(ctx,
			`select id from "trading".securities where ticker='MSFT' and type='stock' limit 1`,
		).Scan(&msftID); err != nil {
			return fmt.Errorf("lookup MSFT id: %w", err)
		}
		if _, err := tx.Exec(ctx, `
            insert into "trading".realized_gains
                (user_id, user_kind, security_id, account_id,
                 quantity, cost_basis_amt, proceeds_amt, currency,
                 gain_native, gain_rsd, realized_at, taxed)
            values
                ($1, 'client', $2, $3, 5,  450.10, 469.99, 'USD',
                 99.45,  10018.00, now() - interval '1 day', false),
                ($1, 'client', $2, $3, 2,  500.00, 480.00, 'USD',
                 -40.00, -4030.32, now() - interval '2 days', false)
            `, clientID, msftID, usdAccountID,
		); err != nil {
			return fmt.Errorf("insert realized_gains: %w", err)
		}
	}

	// Profit Banke — actuary leaderboard fixture (spec p.76). The
	// dashboard's "Učinak aktuara" tab sums positive gain_rsd from
	// realized_gains where user_kind='employee' joined to actuary_info;
	// with no employee rows it renders empty even though the "Pozicije
	// banke u fondovima" tab has the seeded Beta position. Plant a few
	// prior-year, already-taxed gains for the two seeded actuaries so
	// the leaderboard shows the agent ahead of the supervisor.
	//
	// taxed=true + a >366-day-old taxed_at keeps these out of the
	// /portal/porez board and the monthly tax cron regardless of when
	// the seed runs: ListUnpaidGainsForUser filters `not taxed`, and
	// ListTaxAggregates' YTD term needs taxed_at >= date_trunc('year',
	// now()) — an offset of 400 days always lands in a strictly prior
	// calendar year (max day-of-year is 366). Idempotent: skipped once
	// any employee realized_gain exists for the agent.
	var empRG int
	if err := tx.QueryRow(ctx,
		`select count(*) from "trading".realized_gains
		   where user_id = $1 and user_kind = 'employee'`, aktuarID,
	).Scan(&empRG); err != nil {
		return fmt.Errorf("count employee realized_gains: %w", err)
	}
	if empRG == 0 {
		if _, err := tx.Exec(ctx, `
            insert into "trading".realized_gains
                (user_id, user_kind, security_id, account_id,
                 quantity, cost_basis_amt, proceeds_amt, currency,
                 gain_native, gain_rsd, realized_at, taxed, taxed_at)
            values
                -- agent: two wins + one loss (3 trades, +61100 RSD)
                ($1, 'employee', $3, $4, 20, 170.00, 190.50, 'USD',
                 410.00,  41200.00, now() - interval '430 days', true, now() - interval '400 days'),
                ($1, 'employee', $3, $4, 10, 450.10, 469.90, 'USD',
                 198.00,  19900.00, now() - interval '430 days', true, now() - interval '400 days'),
                ($1, 'employee', $3, $4,  5, 190.50, 185.00, 'USD',
                 -27.50, -2762.50, now() - interval '430 days', true, now() - interval '400 days'),
                -- supervisor: one win (1 trade, +7960 RSD)
                ($2, 'employee', $3, $4,  8, 450.10, 460.00, 'USD',
                 79.20,    7960.00, now() - interval '430 days', true, now() - interval '400 days')
            `, aktuarID, supervisorID, aaplID, bankUSDAcctID,
		); err != nil {
			return fmt.Errorf("insert employee realized_gains: %w", err)
		}
	}

	// Pregled naloga ↔ Profit Banke reconciliation. The employee
	// realized_gains above are exactly what the supervisor's Profit
	// Banke leaderboard shows per actuary: count(realized rows) as
	// "broj realizovanih prodaja" + Σ positive gain_rsd as "profit".
	// Plant one matching done SELL order per realized row so that
	// "Pregled naloga", filtered to an actuary, shows precisely that
	// actuary's realized-sale count, with quantities and unit prices
	// (= proceeds_amt) that reconcile to the leaderboard:
	//   agent      → 3 sells (20 + 10 + 5), Σ wins  = 61100 RSD
	//   supervisor → 1 sell  (8),           Σ wins  =  7960 RSD
	// There is no 'done' order status — a finished order is
	// status='approved' + is_done + 0 remaining. Backdated 430 days
	// to align with the realized_at above. Gated by SEED_ORDERS
	// (cypress sets 0: the c3/c4 specs self-fixture their own orders
	// and several read /api/v1/orders?status=pending, which these
	// approved/done rows are never part of). Idempotent on any
	// actuary order already being present.
	if isEnvTruthy(envOr("SEED_ORDERS", "1")) {
		var actOrders int
		if err := tx.QueryRow(ctx,
			`select count(*) from "trading".orders
			   where user_id = $1 and is_actuary = true`, aktuarID,
		).Scan(&actOrders); err != nil {
			return fmt.Errorf("count actuary orders: %w", err)
		}
		if actOrders == 0 {
			if _, err := tx.Exec(ctx, `
            insert into "trading".orders
                (user_id, user_kind, security_id, account_id,
                 order_type, direction, quantity, contract_size,
                 price_per_unit, all_or_none, margin, is_actuary,
                 actor_kind, status, approval_required, approved_by,
                 approved_at, is_done, cancelled, triggered,
                 after_hours, remaining_quantity,
                 last_modification, created_at)
            values
                -- agent: 2 wins + 1 loss == the 3 realized sales above
                ($1, 'employee', $3, $4, 'market', 'sell', 20, 1,
                 190.50, false, false, true, 'employee', 'approved',
                 false, $2, now() - interval '430 days', true, false,
                 false, false, 0,
                 now() - interval '430 days', now() - interval '430 days'),
                ($1, 'employee', $3, $4, 'market', 'sell', 10, 1,
                 469.90, false, false, true, 'employee', 'approved',
                 false, $2, now() - interval '430 days', true, false,
                 false, false, 0,
                 now() - interval '430 days', now() - interval '430 days'),
                ($1, 'employee', $3, $4, 'market', 'sell', 5, 1,
                 185.00, false, false, true, 'employee', 'approved',
                 false, $2, now() - interval '430 days', true, false,
                 false, false, 0,
                 now() - interval '430 days', now() - interval '430 days'),
                -- supervisor: the single +7960 RSD realized sale
                ($2, 'employee', $3, $4, 'market', 'sell', 8, 1,
                 460.00, false, false, true, 'employee', 'approved',
                 false, $2, now() - interval '430 days', true, false,
                 false, false, 0,
                 now() - interval '430 days', now() - interval '430 days')
            `, aktuarID, supervisorID, aaplID, bankUSDAcctID,
			); err != nil {
				return fmt.Errorf("insert actuary done orders: %w", err)
			}
			fmt.Println("seed: actuary done orders planted (Pregled naloga ↔ Profit Banke)")
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit trading seed: %w", err)
	}
	fmt.Printf("seed: trading fixtures created\n"+
		"  usd account:  %s (id=%s, 300000 USD)\n"+
		"  actuary agent:      aktuar@banka.local (id=%s, limit 200000 RSD)\n"+
		"  actuary supervisor: supervizor@banka.local (id=%s)\n"+
		"  exchanges:    XNYS, XLON, XBEL\n"+
		"  stocks:       AAPL, MSFT, GOOGL, VOD, NIS, AMZN, TSLA\n"+
		"  future:       CL (Crude Oil WTI, +90d settlement)\n"+
		"  forex:        EUR/USD\n"+
		"  option:       AAPL-C-190 (call, ATM, +60d expiry)\n"+
		"  profit banke: aktuar +61100 RSD / 3, supervizor +7960 RSD / 1\n",
		usdNumber, usdAccountID, aktuarID, supervisorID)
	return nil
}

// seedOTC plants a handful of c4 OTC negotiations + one accepted
// contract on top of the seeded portfolios so the /banking/otc page
// and "Sklopljeni ugovori" tab have varied data without having to
// click through a negotiation by hand.
//
// Depends on seedTrading already having created klijent's USD trading
// account + AAPL/CL holdings. Mints klijent2's USD trading account
// here so they can be a counterparty.
//
// Threads planted (in addition to the seedTrading set):
//
//  1. Open, 1 iter        — klijent2 buying 3 of klijent's AAPL   (klijent's turn)
//  2. Open, 3 iters       — klijent buying 10 of klijent2's MSFT  (klijent2's turn)
//  3. Withdrawn           — klijent abandoned a 5 GOOGL bid
//  4. Accepted + contract — klijent2 bought 2 of klijent's CL @ $78
//
// Reservation accounting (spec p.68) is baked into the new klijent2
// holding rows at insert time and applied as a guarded bump on
// klijent's existing AAPL/CL rows. All inserts use deterministic
// UUIDs + ON CONFLICT DO NOTHING so re-running the seed is a no-op;
// the marker check up front short-circuits before touching anything.
func seedOTC(ctx context.Context, pool *pgxpool.Pool, clientID, client2ID, adminID string) error {
	if clientID == "" || client2ID == "" || adminID == "" {
		return fmt.Errorf("seedOTC: clientID, client2ID and adminID are required")
	}

	// Idempotent: ensure the seeded OTC-discovery holdings carry a
	// non-zero public_count so the /banking/otc + /portal/otc boards
	// surface something. Without this, every holding sits at the
	// schema default (0) and `where public_count > reserved_count`
	// returns no rows. Runs before the marker short-circuit so dev
	// DBs that were seeded before this fix heal on the next `task
	// seed`; the `public_count < target` guard keeps a hand-edited
	// higher value from being lowered.
	for _, row := range []struct {
		userID, ticker, kind string
		target               int32
	}{
		{clientID, "AAPL", "stock", 8},    // qty 10, reserved 3 ⇒ 5 available
		{client2ID, "MSFT", "stock", 25},  // qty 30, reserved 10 ⇒ 15 available
		{client2ID, "GOOGL", "stock", 10}, // qty 15, reserved 0  ⇒ 10 available
		{client2ID, "AMZN", "stock", 12},  // qty 12, reserved 0  ⇒ 12 available
		{client2ID, "TSLA", "stock", 8},   // qty 8,  reserved 0  ⇒ 8 available
	} {
		if _, err := pool.Exec(ctx, `
            update "trading".portfolio_holdings h
               set public_count = $1
              from "trading".securities s
             where s.id = h.security_id
               and h.user_id = $2
               and s.ticker = $3
               and s.type   = $4
               and h.public_count < $1`,
			row.target, row.userID, row.ticker, row.kind,
		); err != nil {
			return fmt.Errorf("heal public_count %s: %w", row.ticker, err)
		}
	}

	// Skip if already seeded — thread 1's deterministic id is the marker.
	var marker string
	switch err := pool.QueryRow(ctx,
		`select id from "trading".otc_offers
		 where id = 'a1111111-0000-4000-8000-000000000001'`,
	).Scan(&marker); err {
	case nil:
		fmt.Println("seed: otc fixtures already present; skipping")
		return nil
	default:
		if err.Error() != "no rows in result set" {
			return fmt.Errorf("check existing otc seed: %w", err)
		}
	}

	bankCode := envOr("BANK_CODE", "333")
	branch := envOr("BANK_BRANCH", "0001")

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// klijent's USD trading account + AAPL/CL holding rows.
	var clientUSDAcct, clientAAPLHolding, clientCLHolding string
	if err := tx.QueryRow(ctx, `
        select id from "bank".accounts
         where owner_client_id=$1 and currency='USD' and kind='personal_fx'
         limit 1`, clientID).Scan(&clientUSDAcct); err != nil {
		return fmt.Errorf("lookup klijent USD account: %w", err)
	}
	if err := tx.QueryRow(ctx, `
        select h.id from "trading".portfolio_holdings h
        join "trading".securities s on s.id = h.security_id
         where h.user_id=$1 and s.ticker='AAPL' and s.type='stock'
         limit 1`, clientID).Scan(&clientAAPLHolding); err != nil {
		return fmt.Errorf("lookup klijent AAPL holding: %w", err)
	}
	if err := tx.QueryRow(ctx, `
        select h.id from "trading".portfolio_holdings h
        join "trading".securities s on s.id = h.security_id
         where h.user_id=$1 and s.ticker='CL' and s.type='future'
         limit 1`, clientID).Scan(&clientCLHolding); err != nil {
		return fmt.Errorf("lookup klijent CL holding: %w", err)
	}

	// klijent2's USD trading account — create with $50k opening balance
	// if missing. Buyer-flavoured fixture; the premium-tier funds are
	// enough for the seeded threads.
	var client2USDAcct string
	switch err := tx.QueryRow(ctx, `
        select id from "bank".accounts
         where owner_client_id=$1 and currency='USD' and kind='personal_fx'
         limit 1`, client2ID).Scan(&client2USDAcct); err {
	case nil:
		// already there
	default:
		if err.Error() != "no rows in result set" {
			return fmt.Errorf("check klijent2 USD account: %w", err)
		}
		num, err := account.Generate(bankCode, branch, account.TypePersonalFX)
		if err != nil {
			return fmt.Errorf("klijent2 USD account number: %w", err)
		}
		if err := tx.QueryRow(ctx, `
            insert into "bank".accounts
                (number, name, owner_client_id, created_by_employee_id,
                 kind, subtype, currency, status,
                 balance, available_balance, maintenance_fee,
                 daily_limit, monthly_limit)
            values ($1, $2, $3, $4,
                    'personal_fx', 'unspecified', 'USD', 'active',
                    $5, $5, '0', '0', '0')
            returning id`,
			num, "Trgovinski USD", client2ID, adminID, "50000",
		).Scan(&client2USDAcct); err != nil {
			return fmt.Errorf("insert klijent2 USD account: %w", err)
		}
	}

	// Security ids needed by the threads.
	var aaplID, msftID, googlID, clID, amznID, tslaID string
	for _, row := range []struct {
		ticker, kind string
		out          *string
	}{
		{"AAPL", "stock", &aaplID},
		{"MSFT", "stock", &msftID},
		{"GOOGL", "stock", &googlID},
		{"CL", "future", &clID},
		{"AMZN", "stock", &amznID},
		{"TSLA", "stock", &tslaID},
	} {
		if err := tx.QueryRow(ctx,
			`select id from "trading".securities where ticker=$1 and type=$2 limit 1`,
			row.ticker, row.kind,
		).Scan(row.out); err != nil {
			return fmt.Errorf("lookup %s %s: %w", row.ticker, row.kind, err)
		}
	}

	// klijent2 holdings: MSFT (30, 10 reserved by thread 2's open
	// offer, 25 published ⇒ 15 free on the OTC board) and GOOGL (15,
	// no reservation — thread 3 ends withdrawn — 10 published). AMZN
	// (12, all 12 published) and TSLA (8, all 8 published) are pure
	// OTC-board inventory — no seeded thread touches them, they just
	// give "Dostupne hartije" a fuller catalog.
	if _, err := tx.Exec(ctx, `
        insert into "trading".portfolio_holdings
            (id, user_id, user_kind, security_id, account_id,
             quantity, weighted_avg_price, public_count, reserved_count)
        values
            ('11111111-1111-4111-8111-000000000001', $1, 'client', $2, $3, 30, 400.00, 25, 10),
            ('11111111-1111-4111-8111-000000000002', $1, 'client', $4, $3, 15, 170.00, 10,  0),
            ('11111111-1111-4111-8111-000000000003', $1, 'client', $5, $3, 12, 180.00, 12,  0),
            ('11111111-1111-4111-8111-000000000004', $1, 'client', $6, $3,  8, 230.00,  8,  0)
        on conflict (user_id, security_id, account_id) do nothing`,
		client2ID, msftID, client2USDAcct, googlID, amznID, tslaID,
	); err != nil {
		return fmt.Errorf("insert klijent2 holdings: %w", err)
	}

	// Bump klijent's reservations to cover thread 1 (+3 AAPL) and the
	// thread-4 accepted contract (+2 CL). Guarded on free quantity so
	// the bump can't take reserved_count past quantity even if a future
	// caller wedges this in alongside other reservation churn.
	if _, err := tx.Exec(ctx,
		`update "trading".portfolio_holdings
            set reserved_count = reserved_count + 3
          where id = $1 and quantity - reserved_count >= 3`,
		clientAAPLHolding,
	); err != nil {
		return fmt.Errorf("bump klijent AAPL reserved: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`update "trading".portfolio_holdings
            set reserved_count = reserved_count + 2
          where id = $1 and quantity - reserved_count >= 2`,
		clientCLHolding,
	); err != nil {
		return fmt.Errorf("bump klijent CL reserved: %w", err)
	}

	// Thread 1 — open, 1 iter, modified_by=klijent2 → klijent's turn.
	if _, err := tx.Exec(ctx, `
        insert into "trading".otc_offers
            (id, thread_id, security_id, seller_holding_id,
             buyer_id, buyer_kind, buyer_account_id,
             seller_id, seller_kind, seller_account_id,
             quantity, price_per_unit, premium, currency, settlement_date,
             modified_by, status, created_at, updated_at)
        values
            ('a1111111-0000-4000-8000-000000000001',
             'a1111111-0000-4000-8000-000000000001',
             $1, $2,
             $3, 'client', $4,
             $5, 'client', $6,
             3, 195.00, 4.00, 'USD', date '2027-03-31',
             $3, 'open',
             now() - interval '2 days', now() - interval '2 days')
        on conflict (id) do nothing`,
		aaplID, clientAAPLHolding,
		client2ID, client2USDAcct,
		clientID, clientUSDAcct,
	); err != nil {
		return fmt.Errorf("insert otc thread 1: %w", err)
	}

	// Thread 2 — three iters, current open, klijent2's turn.
	// A: klijent opens at $400/$3 (superseded)
	// B: klijent2 counters at $415/$5 (superseded)
	// C: klijent counters at $410/$8 (open) — klijent2's turn.
	if _, err := tx.Exec(ctx, `
        insert into "trading".otc_offers
            (id, thread_id, security_id, seller_holding_id,
             buyer_id, buyer_kind, buyer_account_id,
             seller_id, seller_kind, seller_account_id,
             quantity, price_per_unit, premium, currency, settlement_date,
             modified_by, status, created_at, updated_at)
        values
            ('a2222222-0000-4000-8000-00000000000a',
             'a2222222-0000-4000-8000-00000000000a',
             $1, $2,
             $3, 'client', $4,
             $5, 'client', $6,
             10, 400.00, 3.00, 'USD', date '2027-02-28',
             $3, 'superseded',
             now() - interval '5 days', now() - interval '5 days'),
            ('a2222222-0000-4000-8000-00000000000b',
             'a2222222-0000-4000-8000-00000000000a',
             $1, $2,
             $3, 'client', $4,
             $5, 'client', $6,
             10, 415.00, 5.00, 'USD', date '2027-02-28',
             $5, 'superseded',
             now() - interval '4 days', now() - interval '4 days'),
            ('a2222222-0000-4000-8000-00000000000c',
             'a2222222-0000-4000-8000-00000000000a',
             $1, $2,
             $3, 'client', $4,
             $5, 'client', $6,
             10, 410.00, 8.00, 'USD', date '2027-02-28',
             $3, 'open',
             now() - interval '1 day', now() - interval '1 day')
        on conflict (id) do nothing`,
		msftID, "11111111-1111-4111-8111-000000000001",
		clientID, clientUSDAcct,
		client2ID, client2USDAcct,
	); err != nil {
		return fmt.Errorf("insert otc thread 2: %w", err)
	}

	// Thread 3 — withdrawn. klijent's abandoned 5-GOOGL bid.
	if _, err := tx.Exec(ctx, `
        insert into "trading".otc_offers
            (id, thread_id, security_id, seller_holding_id,
             buyer_id, buyer_kind, buyer_account_id,
             seller_id, seller_kind, seller_account_id,
             quantity, price_per_unit, premium, currency, settlement_date,
             modified_by, status, created_at, updated_at)
        values
            ('a3333333-0000-4000-8000-000000000001',
             'a3333333-0000-4000-8000-000000000001',
             $1, $2,
             $3, 'client', $4,
             $5, 'client', $6,
             5, 168.00, 2.00, 'USD', date '2027-01-31',
             $3, 'withdrawn',
             now() - interval '7 days', now() - interval '6 days')
        on conflict (id) do nothing`,
		googlID, "11111111-1111-4111-8111-000000000002",
		clientID, clientUSDAcct,
		client2ID, client2USDAcct,
	); err != nil {
		return fmt.Errorf("insert otc thread 3: %w", err)
	}

	// Thread 4 — accepted + active contract.
	// A: klijent2 opens at $75/$2 (superseded)
	// B: klijent counters at $78/$3 (accepted) — klijent2 accepted it.
	if _, err := tx.Exec(ctx, `
        insert into "trading".otc_offers
            (id, thread_id, security_id, seller_holding_id,
             buyer_id, buyer_kind, buyer_account_id,
             seller_id, seller_kind, seller_account_id,
             quantity, price_per_unit, premium, currency, settlement_date,
             modified_by, status, created_at, updated_at)
        values
            ('a4444444-0000-4000-8000-00000000000a',
             'a4444444-0000-4000-8000-00000000000a',
             $1, $2,
             $3, 'client', $4,
             $5, 'client', $6,
             2, 75.00, 2.00, 'USD', date '2026-12-31',
             $3, 'superseded',
             now() - interval '10 days', now() - interval '10 days'),
            ('a4444444-0000-4000-8000-00000000000b',
             'a4444444-0000-4000-8000-00000000000a',
             $1, $2,
             $3, 'client', $4,
             $5, 'client', $6,
             2, 78.00, 3.00, 'USD', date '2026-12-31',
             $5, 'accepted',
             now() - interval '9 days', now() - interval '8 days')
        on conflict (id) do nothing`,
		clID, clientCLHolding,
		client2ID, client2USDAcct,
		clientID, clientUSDAcct,
	); err != nil {
		return fmt.Errorf("insert otc thread 4: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        insert into "trading".otc_contracts
            (id, thread_id, security_id, seller_holding_id,
             buyer_id, buyer_kind, buyer_account_id,
             seller_id, seller_kind, seller_account_id,
             quantity, strike_price, premium_paid, currency, settlement_date,
             premium_op_id, status, created_at, updated_at)
        values
            ('c4444444-0000-4000-8000-000000000001',
             'a4444444-0000-4000-8000-00000000000a',
             $1, $2,
             $3, 'client', $4,
             $5, 'client', $6,
             2, 78.00, 3.00, 'USD', date '2026-12-31',
             'c4444444-0000-4000-8000-0000000000ff', 'active',
             now() - interval '8 days', now() - interval '8 days')
        on conflict (id) do nothing`,
		clID, clientCLHolding,
		client2ID, client2USDAcct,
		clientID, clientUSDAcct,
	); err != nil {
		return fmt.Errorf("insert otc contract: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit otc seed: %w", err)
	}
	fmt.Println("seed: otc fixtures created — 4 threads (2 open, 1 withdrawn, 1 accepted) + 1 active contract")
	return nil
}

// seedFunds plants three investment funds with the seeded supervisor
// as manager, then layers mock data on top: client + bank-as-client
// pre-investments, fund-actor stock holdings, and 60 days of
// performance snapshots so /banking/fondovi has populated discovery
// + non-empty Moji fondovi + non-flat charts out of the box.
//
// Each fund gets a paired bank-side RSD liquidity account
// (kind='fund', owner=FundsOwnerID) matching what
// `trading.CreateFund` would mint at runtime.
//
// Idempotency:
//   - fund creation skips funds whose name already exists in
//     trading.investment_funds (per-fund tx).
//   - mock data plants only when none of *these seeded funds* has a
//     client_fund_transactions row yet, so a re-run doesn't
//     double-debit klijent's RSD account. The marker is scoped to the
//     seeded fund ids (not a global count) so unrelated fund activity
//     — e.g. cypress-created funds left behind by a soak run — can't
//     suppress the seed's own fund fixtures, and a dev DB that was
//     wrongly skipped by the old global marker self-heals on the next
//     `make seed`.
func seedFunds(ctx context.Context, pool *pgxpool.Pool, clientID, adminID string) error {
	if clientID == "" || adminID == "" {
		return fmt.Errorf("seedFunds: clientID and adminID are required")
	}
	supervisorEmail := envOr("SEED_SUPERVISOR_EMAIL", "supervizor@banka.local")
	var supervisorID string
	if err := pool.QueryRow(ctx,
		`select id from "user".employees where lower(email)=lower($1)`,
		supervisorEmail,
	).Scan(&supervisorID); err != nil {
		return fmt.Errorf("lookup supervisor %s: %w", supervisorEmail, err)
	}

	bankCode := envOr("BANK_CODE", "333")
	branch := envOr("BANK_BRANCH", "0001")

	funds := []struct {
		name, description, minRSD string
	}{
		{
			"Alfa diverzifikovani fond",
			"Diverzifikovani RSD fond — kombinacija domaćih i regionalnih hartija.",
			"1000",
		},
		{
			"Beta tehnološki fond",
			"Fond fokusiran na vodeće američke tehnološke kompanije (AAPL, MSFT, GOOGL).",
			"5000",
		},
		{
			"Gama konzervativni fond",
			"Konzervativna alokacija za očuvanje kapitala — pretežno RSD likvidnost uz manju izloženost domaćim akcijama.",
			"10000",
		},
	}

	refs := map[string]fundRef{}

	created := 0
	for _, f := range funds {
		var existing fundRef
		switch err := pool.QueryRow(ctx,
			`select id, bank_account_id from "trading".investment_funds where name = $1`,
			f.name,
		).Scan(&existing.id, &existing.bankAccountID); err {
		case nil:
			refs[f.name] = existing
			continue
		default:
			if err.Error() != "no rows in result set" {
				return fmt.Errorf("check existing fund %q: %w", f.name, err)
			}
		}

		tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin tx for %q: %w", f.name, err)
		}
		number, err := account.Generate(bankCode, branch, account.TypeFund)
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("generate fund account number: %w", err)
		}
		var bankAccountID string
		if err := tx.QueryRow(ctx, `
            insert into "bank".accounts
                (number, name, owner_client_id, created_by_employee_id,
                 kind, subtype, currency, status,
                 balance, available_balance, maintenance_fee,
                 daily_limit, monthly_limit)
            values ($1, $2, $3, $4,
                    'fund', 'unspecified', 'RSD', 'active',
                    '0', '0', '0',
                    '0', '0')
            returning id`,
			number, f.name, account.FundsOwnerID, adminID,
		).Scan(&bankAccountID); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("insert fund bank account for %q: %w", f.name, err)
		}
		var fundID string
		if err := tx.QueryRow(ctx, `
            insert into "trading".investment_funds
                (name, description, manager_user_id, bank_account_id,
                 minimum_contribution, total_units, status)
            values ($1, $2, $3, $4, $5::numeric, 0, 'active')
            returning id`,
			f.name, f.description, supervisorID, bankAccountID, f.minRSD,
		).Scan(&fundID); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("insert fund %q: %w", f.name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit fund %q: %w", f.name, err)
		}
		refs[f.name] = fundRef{id: fundID, bankAccountID: bankAccountID}
		created++
		fmt.Printf("seed: fund %q created (id=%s, account=%s, min=%s RSD)\n",
			f.name, fundID, number, f.minRSD)
	}
	if created == 0 {
		fmt.Println("seed: funds already present; skipping creation")
	}

	return seedFundMockData(ctx, pool, clientID, supervisorID, refs)
}

// fundRef pairs a fund's id with its bank-side liquidity account id.
// Carried across seedFunds and seedFundMockData so mock-data planting
// doesn't need to round-trip the DB for ids it already has.
type fundRef struct{ id, bankAccountID string }

// seedFundMockData plants the cross-fund mock state once: per-fund
// pre-investments (klijent + bank-as-client), fund-actor stock
// holdings, and 60 days of performance snapshots — for every seeded
// fund, so /banking/fondovi has a non-flat chart on each one. Marker:
// skips only if one of the *seeded* funds (by id) already has a
// trading.client_fund_transactions row; unrelated funds are ignored.
func seedFundMockData(
	ctx context.Context,
	pool *pgxpool.Pool,
	clientID, supervisorID string,
	refs map[string]fundRef,
) error {
	seededFundIDs := make([]string, 0, len(refs))
	for _, r := range refs {
		seededFundIDs = append(seededFundIDs, r.id)
	}
	var prior int
	if err := pool.QueryRow(ctx,
		`select count(*) from "trading".client_fund_transactions
		  where fund_id::text = any($1)`,
		seededFundIDs,
	).Scan(&prior); err != nil {
		return fmt.Errorf("count existing fund transactions: %w", err)
	}
	if prior > 0 {
		fmt.Println("seed: fund mock data already present; skipping")
		return nil
	}

	// Look up the source accounts + securities we'll touch.
	var clientRSDAcct string
	if err := pool.QueryRow(ctx, `
        select id from "bank".accounts
         where owner_client_id = $1
           and kind = 'personal_checking_rsd'
           and currency = 'RSD'
         limit 1`, clientID).Scan(&clientRSDAcct); err != nil {
		return fmt.Errorf("lookup klijent RSD account: %w", err)
	}
	var bankSystemRSDAcct string
	if err := pool.QueryRow(ctx, `
        select id from "bank".accounts
         where owner_client_id = '00000000-0000-0000-0000-000000000000'
           and kind = 'system'
           and currency = 'RSD'
         limit 1`).Scan(&bankSystemRSDAcct); err != nil {
		return fmt.Errorf("lookup bank system RSD account: %w", err)
	}
	// lookupSecurity returns the stock's security id + the FX ask to
	// convert its native currency into RSD (1.0 for RSD-listed). The
	// caller computes RSD costs from a chosen buy price so seeded
	// holdings can carry a cost basis below current market (visible
	// unrealised profit in the demo).
	lookupSecurity := func(ticker string) (securityID string, fxAskToRSD float64, err error) {
		var native string
		if err = pool.QueryRow(ctx, `
            select s.id, s.currency
              from "trading".securities s
              join "trading".listings l on l.security_id = s.id
             where s.ticker = $1 and s.type = 'stock'
             limit 1`, ticker).Scan(&securityID, &native); err != nil {
			return "", 0, fmt.Errorf("lookup listing %s: %w", ticker, err)
		}
		if native == "RSD" {
			return securityID, 1.0, nil
		}
		var ask string
		if err = pool.QueryRow(ctx,
			`select ask::text from "exchange".fx_rates where "from"=$1 and "to"='RSD'`,
			native,
		).Scan(&ask); err != nil {
			return "", 0, fmt.Errorf("lookup FX %s/RSD: %w", native, err)
		}
		r, rerr := strconv.ParseFloat(ask, 64)
		if rerr != nil {
			return "", 0, fmt.Errorf("parse FX ask %q: %w", ask, rerr)
		}
		return securityID, r, nil
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx for fund mock data: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Tiny RSD-string adder; balances + units are NUMERIC in DB and we
	// don't need exact precision for seed math (DB does the arithmetic).
	type investStep struct {
		fundName            string
		clientID            string
		isBankAsClient      bool
		sourceAccountID     string
		initiatorEmployeeID string
		amountRSD           string
	}
	//
	// Every fund gets a klijent invest (so "Moji fondovi" lists all
	// three) plus a larger supervisor-initiated bank-as-client invest
	// (spec p.75 Napomena 2 — RSD drawn from the bank's house RSD
	// account; surfaces in "Pozicije banke u fondovima"). The
	// bank-side top-up gives each fund enough RSD liquidity to fund
	// its seeded holdings and still leave a believable cash reserve.
	// klijent's per-fund amounts sum to 160000 RSD, unchanged from
	// before, so the klijent RSD account isn't drained any further.
	investSteps := []investStep{
		{"Alfa diverzifikovani fond", clientID, false, clientRSDAcct, "", "60000"},
		{"Beta tehnološki fond", clientID, false, clientRSDAcct, "", "60000"},
		{"Gama konzervativni fond", clientID, false, clientRSDAcct, "", "40000"},
		{"Alfa diverzifikovani fond", account.BankAsClientOwnerID, true, bankSystemRSDAcct, supervisorID, "170000"},
		{"Beta tehnološki fond", account.BankAsClientOwnerID, true, bankSystemRSDAcct, supervisorID, "340000"},
		{"Gama konzervativni fond", account.BankAsClientOwnerID, true, bankSystemRSDAcct, supervisorID, "110000"},
	}

	for _, s := range investSteps {
		ref, ok := refs[s.fundName]
		if !ok {
			return fmt.Errorf("fund %q not present after creation", s.fundName)
		}
		// 1) Debit source account.
		if _, err := tx.Exec(ctx, `
            update "bank".accounts
               set balance = balance - $1::numeric,
                   available_balance = available_balance - $1::numeric
             where id = $2`, s.amountRSD, s.sourceAccountID); err != nil {
			return fmt.Errorf("debit source %s: %w", s.sourceAccountID, err)
		}
		// 2) Credit fund's RSD bank account.
		if _, err := tx.Exec(ctx, `
            update "bank".accounts
               set balance = balance + $1::numeric,
                   available_balance = available_balance + $1::numeric
             where id = $2`, s.amountRSD, ref.bankAccountID); err != nil {
			return fmt.Errorf("credit fund account %s: %w", ref.bankAccountID, err)
		}
		// 3) Mint units at the current unit price. We invest into the
		//    funds before planting any holdings, so total_value ==
		//    total_units throughout and unit_price stays 1 — units
		//    minted == amount_rsd.
		if _, err := tx.Exec(ctx, `
            insert into "trading".client_fund_positions
                (fund_id, client_id, units, total_invested_rsd)
            values ($1, $2, $3::numeric, $3::numeric)
            on conflict (fund_id, client_id) do update set
                units = "trading".client_fund_positions.units + excluded.units,
                total_invested_rsd =
                    "trading".client_fund_positions.total_invested_rsd
                    + excluded.total_invested_rsd,
                updated_at = now()`,
			ref.id, s.clientID, s.amountRSD,
		); err != nil {
			return fmt.Errorf("upsert client_fund_positions: %w", err)
		}
		if _, err := tx.Exec(ctx, `
            update "trading".investment_funds
               set total_units = total_units + $1::numeric, updated_at = now()
             where id = $2`, s.amountRSD, ref.id); err != nil {
			return fmt.Errorf("bump fund total_units: %w", err)
		}
		// 4) Audit row, completed.
		var initiator any
		if s.initiatorEmployeeID != "" {
			initiator = s.initiatorEmployeeID
		}
		if _, err := tx.Exec(ctx, `
            insert into "trading".client_fund_transactions
                (fund_id, client_id, initiator_employee_id,
                 amount_rsd, units_delta, source_or_dest_account_id,
                 is_inflow, status)
            values ($1, $2, $3, $4::numeric, $4::numeric, $5, true, 'completed')`,
			ref.id, s.clientID, initiator, s.amountRSD, s.sourceAccountID,
		); err != nil {
			return fmt.Errorf("insert client_fund_transactions: %w", err)
		}
	}

	// Fund-actor holdings, themed per fund so each one's chart reflects
	// a plausible portfolio. buyPriceNative is set deliberately below
	// the seeded listing price (NIS 850, AAPL 190.50, MSFT 450.10,
	// GOOGL 175.30) so the fund carries an unrealised gain — Moji
	// fondovi / Profit show green out of the box instead of a flat
	// break-even. The fund's RSD account is debited by the actual cost
	// (buyPrice × qty × FX); weighted_avg_price stores the native-
	// currency cost basis.
	type holdingStep struct {
		fundName       string
		ticker         string
		quantity       int
		buyPriceNative float64
	}
	// Candidates by theme:
	//   Alfa — diverzifikovani: domaća RSD akcija + jedna američka.
	//   Beta — tehnološki: baš AAPL/MSFT/GOOGL iz opisa fonda.
	//   Gama — konzervativni: mala domaća izloženost, ostalo RSD keš.
	// NIS is RSD-listed so it resolves without the FX feed and is
	// always kept; USD tickers are best-effort — skipped (not fatal)
	// when the FX feed hasn't populated USD→RSD yet, in which case the
	// snapshot loop below walks the fund's liquid line instead so the
	// chart still isn't flat.
	candidates := []holdingStep{
		{"Alfa diverzifikovani fond", "NIS", 100, 760.00},
		{"Alfa diverzifikovani fond", "AAPL", 5, 160.00},
		{"Beta tehnološki fond", "AAPL", 6, 165.00},
		{"Beta tehnološki fond", "MSFT", 4, 408.00},
		{"Beta tehnološki fond", "GOOGL", 6, 150.00},
		{"Gama konzervativni fond", "NIS", 30, 800.00},
	}
	holdings := make([]holdingStep, 0, len(candidates))
	for _, c := range candidates {
		if _, _, ferr := lookupSecurity(c.ticker); ferr != nil {
			fmt.Printf("seed: skipping fund holding %s/%s (FX not wired: %v)\n",
				c.fundName, c.ticker, ferr)
			continue
		}
		holdings = append(holdings, c)
	}

	for _, h := range holdings {
		ref, ok := refs[h.fundName]
		if !ok {
			return fmt.Errorf("fund %q not present for holding step", h.fundName)
		}
		secID, fxAsk, err := lookupSecurity(h.ticker)
		if err != nil {
			return fmt.Errorf("lookup %s: %w", h.ticker, err)
		}
		totalRSD := strconv.FormatFloat(
			float64(h.quantity)*h.buyPriceNative*fxAsk, 'f', 4, 64)
		// Debit fund's RSD account by the RSD cost.
		if _, err := tx.Exec(ctx, `
            update "bank".accounts
               set balance = balance - $1::numeric,
                   available_balance = available_balance - $1::numeric
             where id = $2`, totalRSD, ref.bankAccountID); err != nil {
			return fmt.Errorf("debit fund account for %s: %w", h.ticker, err)
		}
		if _, err := tx.Exec(ctx, `
            insert into "trading".portfolio_holdings
                (user_id, user_kind, security_id, account_id,
                 quantity, weighted_avg_price)
            values ($1, 'fund', $2, $3, $4, $5::numeric)`,
			ref.id, secID, ref.bankAccountID, h.quantity,
			strconv.FormatFloat(h.buyPriceNative, 'f', 4, 64),
		); err != nil {
			return fmt.Errorf("insert fund holding %s: %w", h.ticker, err)
		}
	}

	// Performance snapshots: 60 daily rows per *every* seeded fund,
	// ending at "now", so /banking/fondovi shows a non-flat chart on
	// each. The FE charts total_value = liquid + holdings. Normally we
	// hold liquid flat and walk holdings_value with a deterministic
	// ±2%/day step (seeded by fund_id) terminating at the fund's
	// current holdings_value_rsd. If a fund ended up with no holdings
	// (a USD-only fund seeded while the FX feed was down), we instead
	// walk the liquid line so total_value still moves — no fund gets a
	// dead-flat chart.
	const snapDays = 60
	for name, ref := range refs {
		// Today's state straight out of the DB so we don't drift from
		// the inserts above.
		var liquidStr, holdingsStr string
		if err := tx.QueryRow(ctx, `
            select a.available_balance::text,
                   coalesce(sum(h.quantity * h.weighted_avg_price *
                                case when s.currency = 'RSD' then 1
                                     else (select ask from "exchange".fx_rates
                                            where "from" = s.currency and "to" = 'RSD')
                                end)::text, '0') as holdings_rsd
              from "bank".accounts a
              left join "trading".portfolio_holdings h
                     on h.account_id = a.id and h.user_kind = 'fund'
              left join "trading".securities s on s.id = h.security_id
             where a.id = $1
             group by a.available_balance`,
			ref.bankAccountID,
		).Scan(&liquidStr, &holdingsStr); err != nil {
			return fmt.Errorf("read current fund state %q: %w", name, err)
		}
		liquid, _ := strconv.ParseFloat(liquidStr, 64)
		holdingsNow, _ := strconv.ParseFloat(holdingsStr, 64)

		h := fnv.New64a()
		_, _ = h.Write([]byte(ref.id))
		rng := rand.New(rand.NewPCG(h.Sum64(), h.Sum64()^0x9e3779b97f4a7c15))

		// Walk whichever line carries this fund's value. With holdings
		// it's holdings_value (liquid held flat); a holdings-less fund
		// falls back to walking liquid so the chart still isn't flat.
		walkLiquid := holdingsNow <= 0
		terminal := holdingsNow
		if walkLiquid {
			terminal = liquid
		}

		values := make([]float64, snapDays+1)
		values[snapDays] = terminal
		for i := snapDays - 1; i >= 0; i-- {
			step := 1.0 + (rng.Float64()*0.04 - 0.018) // ±2% with mild uptrend
			if values[i+1] > 0 {
				values[i] = values[i+1] / step
			} else {
				values[i] = 0
			}
		}
		now := time.Now().UTC()
		for i := 0; i <= snapDays; i++ {
			at := now.AddDate(0, 0, -(snapDays - i))
			liquidAt, holdingsAt := liquid, values[i]
			if walkLiquid {
				liquidAt, holdingsAt = values[i], 0
			}
			if _, err := tx.Exec(ctx, `
                insert into "trading".fund_performance_snapshots
                    (fund_id, snapshot_at, liquid_rsd, holdings_value_rsd)
                values ($1, $2, $3::numeric, $4::numeric)
                on conflict (fund_id, snapshot_at) do nothing`,
				ref.id, at,
				strconv.FormatFloat(liquidAt, 'f', 4, 64),
				strconv.FormatFloat(holdingsAt, 'f', 4, 64),
			); err != nil {
				return fmt.Errorf("insert snapshot %q: %w", name, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit fund mock data: %w", err)
	}
	fmt.Printf("seed: fund mock data created — %d invests, %d holdings, %d×%d snapshots\n",
		len(investSteps), len(holdings), len(refs), snapDays+1)
	return nil
}

// dedupePerms returns perms with duplicates removed, preserving first-
// occurrence order. We compose the actuary fixtures' permission sets
// from RoleEmployeeAgent + RoleEmployeeActuary{Agent,Supervisor},
// which overlap on Actuary in the supervisor case; the user.employees
// permissions column has no uniqueness guarantee but agent code paths
// expect at-most-once entries.
func dedupePerms(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, p := range in {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// bizID looks up the business RSD account for the client we just
// inserted in the same tx. Used by the rejected loan_requests row.
// We do this rather than thread another return value through every
// account-creation call because only loan_requests cares.
func bizID(tx pgx.Tx, ctx context.Context, clientID string) string {
	var id string
	_ = tx.QueryRow(ctx,
		`select id from "bank".accounts
		 where owner_client_id=$1 and kind='business_checking_rsd' limit 1`,
		clientID).Scan(&id)
	return id
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// isEnvTruthy treats "0", "false", "off", "no" (case-insensitive) as
// false and everything else as true. Used for opt-out env switches
// that default on.
func isEnvTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}
