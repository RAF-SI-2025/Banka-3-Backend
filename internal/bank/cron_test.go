package bank

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRunMonthlyVariableRateUpdate(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()

	// getApprovedVariableLoans
	loanRows := sqlmock.NewRows([]string{
		"id", "account_id", "amount", "currency_id", "installments",
		"nominal_rate", "interest_rate", "date_signed", "date_end", "monthly_payment",
		"next_payment_due", "remaining_debt", "type", "loan_status", "interest_rate_type",
	}).AddRow(
		int64(1), int64(1), int64(1_000_000_00), int64(1), int64(24),
		float32(8.0), float32(8.0), now, now.AddDate(2, 0, 0), int64(4_522_700),
		now.AddDate(0, 1, 0), int64(900_000_00), "cash", "approved", "variable",
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "loans" WHERE interest_rate_type = $1 AND loan_status = $2`)).
		WithArgs("variable", "approved").
		WillReturnRows(loanRows)

	// getCurrencyLabelByID
	currencyRows := sqlmock.NewRows([]string{
		"id", "label", "name", "symbol", "countries", "description", "active",
	}).AddRow(int64(1), "RSD", "Serbian Dinar", "din", "Serbia", "Serbian Dinar", true)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "currencies" WHERE "currencies"."id" = $1`)).
		WithArgs(int64(1), 1).
		WillReturnRows(currencyRows)

	// countPaidInstallments
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "loan_installment" WHERE loan_id = $1 AND status = $2`)).
		WithArgs(int64(1), "paid").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(4)))

	// UPDATE loan
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "loans"`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	server.RunMonthlyVariableRateUpdate()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRunDailyInstallmentCollection_Success(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()
	today := now.Truncate(24 * time.Hour)

	// getLoansDueForCollection
	loanRows := sqlmock.NewRows([]string{
		"id", "account_id", "amount", "currency_id", "installments",
		"nominal_rate", "interest_rate", "date_signed", "date_end", "monthly_payment",
		"next_payment_due", "remaining_debt", "type", "loan_status", "interest_rate_type",
	}).AddRow(
		int64(1), int64(1), int64(10_000_00), int64(1), int64(12),
		float32(8.0), float32(8.0), now.AddDate(-1, 0, 0), now.AddDate(0, 11, 0), int64(86_988),
		today, int64(500_000), "cash", "approved", "fixed",
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "loans" WHERE next_payment_due <= $1 AND loan_status IN ($2,$3)`)).
		WithArgs(sqlmock.AnyArg(), "approved", "late").
		WillReturnRows(loanRows)

	// processLoanPayment: create installment
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "loan_installment"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectCommit()

	// processLoanPayment: update loan
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "loans"`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	// Retry late installments query (none)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "loan_installment" WHERE status = $1 AND due_date <= $2`)).
		WithArgs("late", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "loan_id", "installment_amount", "interest_rate",
			"currency_id", "due_date", "paid_date", "status",
		}))

	server.RunDailyInstallmentCollection()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRunDailyInstallmentCollection_FullPayoff(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()
	today := now.Truncate(24 * time.Hour)

	// getLoansDueForCollection - remaining_debt equals monthly_payment (last installment)
	loanRows := sqlmock.NewRows([]string{
		"id", "account_id", "amount", "currency_id", "installments",
		"nominal_rate", "interest_rate", "date_signed", "date_end", "monthly_payment",
		"next_payment_due", "remaining_debt", "type", "loan_status", "interest_rate_type",
	}).AddRow(
		int64(2), int64(1), int64(10_000_00), int64(1), int64(12),
		float32(8.0), float32(8.0), now.AddDate(-1, 0, 0), now.AddDate(0, 0, 0), int64(86_988),
		today, int64(86_988), "cash", "approved", "fixed",
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "loans" WHERE next_payment_due <= $1 AND loan_status IN ($2,$3)`)).
		WithArgs(sqlmock.AnyArg(), "approved", "late").
		WillReturnRows(loanRows)

	// processLoanPayment: create installment
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "loan_installment"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(12)))
	mock.ExpectCommit()

	// processLoanPayment: update loan (should set loan_status=paid since remaining_debt goes to 0)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "loans"`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	// Retry late installments query (none)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "loan_installment" WHERE status = $1 AND due_date <= $2`)).
		WithArgs("late", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "loan_id", "installment_amount", "interest_rate",
			"currency_id", "due_date", "paid_date", "status",
		}))

	server.RunDailyInstallmentCollection()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
