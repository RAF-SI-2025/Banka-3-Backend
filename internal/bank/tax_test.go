package bank

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestIsLastOfMonth pins the cron filter so the capital-gains sweep doesn't
// drift onto the wrong day around month boundaries (Feb 28/29, 30/31 months).
func TestIsLastOfMonth(t *testing.T) {
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"jan 31", time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), true},
		{"jan 30", time.Date(2026, 1, 30, 0, 0, 0, 0, time.UTC), false},
		{"feb 28 non-leap", time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC), true},
		{"feb 28 leap", time.Date(2024, 2, 28, 0, 0, 0, 0, time.UTC), false},
		{"feb 29 leap", time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), true},
		{"apr 30", time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC), true},
		{"apr 29", time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC), false},
		{"dec 31", time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isLastOfMonth(c.t); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestCollectCapitalGains_HappyPath_RSD walks through the simplest collection
// shape: one RSD-denominated bucket. Confirms the lookup → bucket scan →
// per-account debit/credit/mark-paid cycle hits all the right rows and that
// the result counters add up.
func TestCollectCapitalGains_HappyPath_RSD(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	// State RSD account lookup
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT a.id, a.number FROM accounts a`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "number"}).
			AddRow(int64(99), "333000100000099910"))

	// Bucket scan: one account owes 5000 RSD
	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT account_id, SUM(tax_due) AS total FROM "capital_gains" WHERE paid_at IS NULL AND period = $1 GROUP BY "account_id" HAVING SUM(tax_due) > 0`)).
		WithArgs("2026-04").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "total"}).
			AddRow(int64(7), int64(5000)))

	// Per-account tx
	mock.ExpectBegin()
	// FOR UPDATE select on the account
	mock.ExpectQuery(`SELECT \* FROM "accounts" WHERE "accounts"\."id" = \$1 .* FOR UPDATE`).
		WithArgs(int64(7), 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "number", "name", "owner", "company_id", "balance",
			"created_by", "created_at", "valid_until", "currency", "active",
			"owner_type", "account_type", "maintainance_cost",
			"daily_limit", "monthly_limit", "daily_expenditure", "monthly_expenditure",
		}).AddRow(
			int64(7), "111", "client RSD", int64(1), nil, int64(100_000),
			int64(1), time.Now(), time.Now().AddDate(1, 0, 0), "RSD", true,
			"personal", "checking", int64(0),
			nil, nil, nil, nil,
		))

	// Debit user account
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE accounts SET balance = balance - $1`)).
		WithArgs(int64(5000), int64(7), int64(5000)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Credit state account
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE accounts SET balance = balance + $1`)).
		WithArgs(int64(5000), int64(99)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Mark rows paid
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "capital_gains" SET "paid_at"=$1 WHERE (account_id = $2 AND paid_at IS NULL) AND period = $3`)).
		WithArgs(sqlmock.AnyArg(), int64(7), "2026-04").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	res, err := server.CollectCapitalGains("2026-04")
	if err != nil {
		t.Fatalf("CollectCapitalGains: %v", err)
	}
	if res.AccountsPaid != 1 {
		t.Errorf("AccountsPaid: got %d, want 1", res.AccountsPaid)
	}
	if res.RowsPaid != 3 {
		t.Errorf("RowsPaid: got %d, want 3", res.RowsPaid)
	}
	if res.CollectedRSD != 5000 {
		t.Errorf("CollectedRSD: got %d, want 5000", res.CollectedRSD)
	}
	if res.TotalDebtRSD != 5000 {
		t.Errorf("TotalDebtRSD: got %d, want 5000", res.TotalDebtRSD)
	}
	if res.Insufficient != 0 {
		t.Errorf("Insufficient: got %d, want 0", res.Insufficient)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestCollectCapitalGains_InsufficientFunds confirms the per-account
// transaction is rolled back when the placer can't cover the tax, and that
// the result counter increments instead of erroring out so the surrounding
// loop continues with the remaining accounts.
func TestCollectCapitalGains_InsufficientFunds(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT a.id, a.number FROM accounts a`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "number"}).
			AddRow(int64(99), "333000100000099910"))

	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT account_id, SUM(tax_due) AS total FROM "capital_gains" WHERE paid_at IS NULL AND period = $1 GROUP BY "account_id" HAVING SUM(tax_due) > 0`)).
		WithArgs("2026-04").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "total"}).
			AddRow(int64(7), int64(50_000)))

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT \* FROM "accounts" WHERE "accounts"\."id" = \$1 .* FOR UPDATE`).
		WithArgs(int64(7), 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "number", "name", "owner", "company_id", "balance",
			"created_by", "created_at", "valid_until", "currency", "active",
			"owner_type", "account_type", "maintainance_cost",
			"daily_limit", "monthly_limit", "daily_expenditure", "monthly_expenditure",
		}).AddRow(
			int64(7), "111", "client RSD", int64(1), nil, int64(10_000),
			int64(1), time.Now(), time.Now().AddDate(1, 0, 0), "RSD", true,
			"personal", "checking", int64(0),
			nil, nil, nil, nil,
		))
	// Balance check fails → 0 rows affected
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE accounts SET balance = balance - $1`)).
		WithArgs(int64(50_000), int64(7), int64(50_000)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	res, err := server.CollectCapitalGains("2026-04")
	if err != nil {
		t.Fatalf("CollectCapitalGains: %v", err)
	}
	if res.AccountsPaid != 0 {
		t.Errorf("AccountsPaid: got %d, want 0", res.AccountsPaid)
	}
	if res.Insufficient != 1 {
		t.Errorf("Insufficient: got %d, want 1", res.Insufficient)
	}
	if res.CollectedRSD != 0 {
		t.Errorf("CollectedRSD: got %d, want 0", res.CollectedRSD)
	}
	if res.TotalDebtRSD != 50_000 {
		t.Errorf("TotalDebtRSD: got %d, want 50000", res.TotalDebtRSD)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestCollectCapitalGains_StateAccountMissing surfaces the failure mode where
// the seed didn't run (or the tax_code=1 row was deleted): the cron should
// bail instead of silently doing nothing.
func TestCollectCapitalGains_StateAccountMissing(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT a.id, a.number FROM accounts a`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "number"}))

	if _, err := server.CollectCapitalGains("2026-04"); err == nil {
		t.Fatalf("expected error when state account is missing")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
