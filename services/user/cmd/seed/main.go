// Command seed plants development fixtures across the whole stack.
// Three layers, all idempotent and unconditional:
//
//	1. bootstrap admin (so the system has someone who can create
//	   employees — the spec gates that on admin)
//	2. test client klijent@banka.local
//	3. bank fixtures hung off the test client: two companies, three
//	   accounts (RSD personal / EUR personal / RSD business), two
//	   cards (active Visa + blocked Mastercard), two loans (active +
//	   paid_off), and two loan requests (pending + rejected)
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
//
// The default password meets the spec's complexity rules (8–32 chars,
// ≥2 digits, ≥1 upper, ≥1 lower) but should be changed in any shared
// environment. Print the credentials on success so a fresh dev knows
// what to log in with.
package main

import (
	"context"
	"fmt"
	"os"
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

	// Second client — useful for testing flows that need two distinct
	// client logins (e.g. inter-client payments). No bank fixtures
	// hung off them; the admin can mint accounts via the portal if
	// needed. Idempotent on email like the first.
	if _, err := seedClient(ctx, pool,
		envOr("SEED_CLIENT2_EMAIL", "klijent2@banka.local"),
		envOr("SEED_CLIENT2_PASSWORD", "Klijent123!"),
		"Drugi", "Klijent", "+381111000222"); err != nil {
		return fmt.Errorf("seed second client: %w", err)
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
		fmt.Printf("seed: client already exists (id=%s, email=%s); skipping\n", existing, email)
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
            true,
            array['client.read','account.read','card.read','card.write','payment.write','loan.read','loan.write']
        ) returning id`
	var id string
	if err := pool.QueryRow(ctx, q, email, hash, firstName, lastName, phone).Scan(&id); err != nil {
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
// can be down when seed runs (we do this after `task migrate` but
// before the service is necessarily up).
func seedBank(ctx context.Context, pool *pgxpool.Pool, clientID, adminID string) error {
	if clientID == "" || adminID == "" {
		return fmt.Errorf("seedBank: clientID and adminID are required")
	}
	// Idempotency. Two checks because cypress's resetBackend truncates
	// the user schema (re-mints clientID) but leaves bank rows; we
	// don't want the second seed run to crash on the registry_id unique
	// constraint just because the user side bounced.
	//
	//   a) accounts owned by this client → fixtures already planted
	//      against this client; skip.
	//   b) any row in bank.companies with the fixture registry_ids →
	//      fixtures planted against a previous (now-deleted) client;
	//      also skip rather than try to re-insert.
	var have int
	if err := pool.QueryRow(ctx,
		`select count(*) from "bank".accounts where owner_client_id=$1`, clientID).Scan(&have); err != nil {
		return fmt.Errorf("count accounts: %w", err)
	}
	if have > 0 {
		fmt.Println("seed: bank fixtures already present for client; skipping")
		return nil
	}
	if err := pool.QueryRow(ctx,
		`select count(*) from "bank".companies where registry_id in ('12345678','23456789')`).Scan(&have); err != nil {
		return fmt.Errorf("count fixture companies: %w", err)
	}
	if have > 0 {
		fmt.Println("seed: bank fixtures already present (orphaned); skipping")
		return nil
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
