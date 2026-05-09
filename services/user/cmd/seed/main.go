// Command seed plants development fixtures into the user service's
// database. Today this means: a bootstrap admin so the system can be
// brought up from zero (the spec says only an admin can create
// employees, so without this nobody could log in).
//
// Idempotent: if any employee already has the `admin` permission, the
// program does nothing. Re-run after a `task migrate` and you'll either
// create the admin (first run) or no-op (subsequent runs).
//
// Configuration:
//
//	DATABASE_URL    — required; standard pgx DSN
//	SEED_ADMIN_EMAIL    (default admin@banka.local)
//	SEED_ADMIN_PASSWORD (default Admin123!) — must satisfy spec policy
//	SEED_ADMIN_USERNAME (default admin)
//	SEED_CLIENT         (set to "true" to also plant a test client)
//	SEED_CLIENT_EMAIL    (default klijent@banka.local)
//	SEED_CLIENT_PASSWORD (default Klijent123!)
//	SEED_C2             (set to "true" to also plant a c2 fixture set:
//	                     company, accounts, card, loan — for the seeded
//	                     client. Skipped silently if the bank schema
//	                     hasn't been migrated yet, so the same flag is
//	                     safe to leave on across c1-only and c2 runs.
//	                     Implies SEED_CLIENT=true.)
//	BANK_CODE           (default 333)  — used to mint account numbers
//	BANK_BRANCH         (default 0001) — see pkg/account
//	BANK_CVV_PEPPER     (default "dev-pepper") — see pkg/cvv
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

	// Optional c2 fixture: a known client account for cypress / manual
	// browser testing. Off by default to avoid surprising someone who
	// only wants the admin; turn on with SEED_CLIENT=true. Runs whether
	// or not the admin was just created — re-running with SEED_CLIENT=true
	// after the admin already exists must still plant the client.
	wantClient := envOr("SEED_CLIENT", "") == "true"
	wantC2 := envOr("SEED_C2", "") == "true"
	if wantC2 {
		// SEED_C2 implies SEED_CLIENT — the c2 fixtures are tied to a
		// concrete client, and minting them against a non-seeded
		// client would surprise whoever runs `task seed`.
		wantClient = true
	}

	var clientID string
	if wantClient {
		id, err := seedClient(ctx, pool)
		if err != nil {
			return fmt.Errorf("seed client: %w", err)
		}
		clientID = id
	}

	if wantC2 {
		if !bankSchemaReady(ctx, pool) {
			fmt.Println("seed: SEED_C2 requested but bank schema not migrated; skipping c2 fixtures")
		} else if err := seedC2(ctx, pool, clientID, adminID); err != nil {
			return fmt.Errorf("seed c2: %w", err)
		}
	}
	return nil
}

// seedClient plants a single fully-activated client. Idempotent on
// email. Returns the (possibly-existing) client UUID so callers can
// hang c2 fixtures off it.
func seedClient(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	email := envOr("SEED_CLIENT_EMAIL", "klijent@banka.local")
	password := envOr("SEED_CLIENT_PASSWORD", "Klijent123!")
	if err := passwords.ValidateComplexity(password); err != nil {
		return "", fmt.Errorf("SEED_CLIENT_PASSWORD: %w", err)
	}
	var existing string
	switch err := pool.QueryRow(ctx,
		`select id from "user".clients where lower(email) = lower($1)`, email).Scan(&existing); err {
	case nil:
		fmt.Printf("seed: client already exists (id=%s); skipping\n", existing)
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
            'Test', 'Klijent', '1990-01-01', 'male', '+381111000111', 'Beograd',
            true,
            array['client.read','account.read','card.read','card.write','payment.write','loan.read','loan.write']
        ) returning id`
	var id string
	if err := pool.QueryRow(ctx, q, email, hash).Scan(&id); err != nil {
		return "", fmt.Errorf("insert client: %w", err)
	}
	fmt.Printf("seed: client created (id=%s)\n  email:    %s\n  password: %s\n", id, email, password)
	return id, nil
}

// bankSchemaReady reports whether the c2 migrations have been applied.
// We sniff for bank.accounts; if it's missing we skip the c2 fixtures
// rather than crashing, so SEED_C2=true is safe to leave on across c1
// and c2 stack runs.
func bankSchemaReady(ctx context.Context, pool *pgxpool.Pool) bool {
	var n int
	err := pool.QueryRow(ctx,
		`select count(*) from information_schema.tables
		 where table_schema='bank' and table_name='accounts'`).Scan(&n)
	return err == nil && n > 0
}

// seedC2 plants a small but representative c2 dataset for the seeded
// client: one company, two accounts (RSD + EUR), one active card on
// the RSD account, and one approved cash loan with the first
// installment already paid. Idempotent: if the client already has any
// account, the function no-ops.
//
// We write directly to the bank schema (same convention as seedClient
// vs the user schema) instead of going through the bank service —
// cuts the seed dependency surface to plain pgx, and the bank service
// can be down when seed runs (we do this after `task migrate` but
// before the service is necessarily up).
func seedC2(ctx context.Context, pool *pgxpool.Pool, clientID, adminID string) error {
	if clientID == "" {
		return fmt.Errorf("seedC2 requires a client id (set SEED_CLIENT=true)")
	}
	if adminID == "" {
		return fmt.Errorf("seedC2 requires an admin id (admin seed must run first)")
	}
	// Idempotency: if this client already has an account, presume the
	// fixtures are in place. Cheaper than checking each table.
	var have int
	if err := pool.QueryRow(ctx,
		`select count(*) from "bank".accounts where owner_client_id=$1`, clientID).Scan(&have); err != nil {
		return fmt.Errorf("count accounts: %w", err)
	}
	if have > 0 {
		fmt.Println("seed: c2 fixtures already present for client; skipping")
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

	// 1) Company owned by the client.
	var companyID string
	if err := tx.QueryRow(ctx, `
        insert into "bank".companies
            (name, registry_id, tax_id, activity_code, address, owner_client_id)
        values ($1,$2,$3,$4,$5,$6)
        returning id`,
		"Klikovac DOO", "12345678", "987654321", "62.01", "Beograd", clientID,
	).Scan(&companyID); err != nil {
		return fmt.Errorf("insert company: %w", err)
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
	if _, err := insertAccount(eurNumber, "Devizni EUR", "personal_fx", "unspecified", "EUR",
		nil, "1500", "1000", "5000", "0"); err != nil {
		return fmt.Errorf("insert eur account: %w", err)
	}
	if _, err := insertAccount(bizNumber, "Poslovni RSD", "business_checking_rsd", "doo", "RSD",
		&companyID, "500000", "300000", "3000000", "1000"); err != nil {
		return fmt.Errorf("insert business account: %w", err)
	}

	// 3) Active Visa card on the RSD account. CVV 123 (we never read
	// it back; the field is just non-null per schema).
	cvvHash, err := cvv.Hash("123", pepper)
	if err != nil {
		return fmt.Errorf("hash cvv: %w", err)
	}
	cardNumber := "4111111111111111" // a recognisable Visa test PAN
	if _, err := tx.Exec(ctx, `
        insert into "bank".cards
            (number, cvv_hash, brand, name, account_id, card_limit, expires_at, status)
        values ($1,$2,'visa','Debit',$3,$4, now() + interval '4 years','active')`,
		cardNumber, cvvHash, rsdID, "100000",
	); err != nil {
		return fmt.Errorf("insert card: %w", err)
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

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Printf("seed: c2 fixtures created\n  company:        %s (Klikovac DOO)\n"+
		"  rsd account:    %s (id=%s)\n  eur account:    %s\n"+
		"  business acct:  %s\n  card:           %s (Visa, CVV=123)\n"+
		"  loan:           LN-DEV-0001 (id=%s, principal=%s RSD, 12 installments)\n",
		companyID, rsdNumber, rsdID, eurNumber, bizNumber, cardNumber, loanID, principal)
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
