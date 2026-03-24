package bank

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newGormTestServer(t *testing.T) (*Server, sqlmock.Sqlmock) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	return NewServer(nil, gormDB), mock
}

func loanRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"loan_number", "loan_type", "account_number", "loan_amount", "repayment_period", "nominal_rate", "effective_rate",
		"agreement_date", "maturity_date", "next_installment_amount", "next_installment_date", "remaining_debt", "currency", "status",
	})
}

func TestGetLoansSuccess(t *testing.T) {
	server, mock := newGormTestServer(t)

	mock.ExpectQuery(regexp.QuoteMeta(`FROM "loans" JOIN accounts ON accounts.id = loans.account_id JOIN clients ON clients.id = accounts.owner JOIN currencies ON currencies.id = loans.currency_id WHERE clients.email = $1 ORDER BY loans.id DESC`)).
		WithArgs("client@test.com").
		WillReturnRows(loanRows().AddRow("55", "cash", "ACC-1", 100000.0, 24, 5.2, 0.0, "2025-01-01", "2027-01-01", 4500.0, "2026-04-01", 80000.0, "RSD", "approved"))

	resp, err := server.GetLoans(context.Background(), &bankpb.GetLoansRequest{ClientEmail: "client@test.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Loans) != 1 {
		t.Fatalf("expected 1 loan, got %d", len(resp.Loans))
	}
	if resp.Loans[0].LoanNumber != "55" || resp.Loans[0].Status != "approved" {
		t.Fatalf("unexpected loan response: %+v", resp.Loans[0])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetLoansValidation(t *testing.T) {
	server, mock := newGormTestServer(t)

	_, err := server.GetLoans(context.Background(), &bankpb.GetLoansRequest{ClientEmail: ""})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", status.Code(err))
	}

	_, err = server.GetLoans(context.Background(), &bankpb.GetLoansRequest{ClientEmail: "a@b.rs", LoanType: "BAD"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for loan type, got %v", status.Code(err))
	}

	_, err = server.GetLoans(context.Background(), &bankpb.GetLoansRequest{ClientEmail: "a@b.rs", Status: "BAD"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for status, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetLoanByNumberValidationAndNotFound(t *testing.T) {
	server, mock := newGormTestServer(t)

	_, err := server.GetLoanByNumber(context.Background(), &bankpb.GetLoanByNumberRequest{ClientEmail: "", LoanNumber: "1"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", status.Code(err))
	}

	_, err = server.GetLoanByNumber(context.Background(), &bankpb.GetLoanByNumberRequest{ClientEmail: "a@b.rs", LoanNumber: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for empty loan number, got %v", status.Code(err))
	}

	_, err = server.GetLoanByNumber(context.Background(), &bankpb.GetLoanByNumberRequest{ClientEmail: "a@b.rs", LoanNumber: "abc"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for invalid loan number, got %v", status.Code(err))
	}

	mock.ExpectQuery(regexp.QuoteMeta(`FROM "loans" JOIN accounts ON accounts.id = loans.account_id JOIN clients ON clients.id = accounts.owner JOIN currencies ON currencies.id = loans.currency_id WHERE clients.email = $1 AND loans.id = $2`)).
		WithArgs("a@b.rs", int64(999), sqlmock.AnyArg()).
		WillReturnRows(loanRows())

	_, err = server.GetLoanByNumber(context.Background(), &bankpb.GetLoanByNumberRequest{ClientEmail: "a@b.rs", LoanNumber: "999"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetLoanByNumberSuccess(t *testing.T) {
	server, mock := newGormTestServer(t)

	mock.ExpectQuery(regexp.QuoteMeta(`FROM "loans" JOIN accounts ON accounts.id = loans.account_id JOIN clients ON clients.id = accounts.owner JOIN currencies ON currencies.id = loans.currency_id WHERE clients.email = $1 AND loans.id = $2`)).
		WithArgs("a@b.rs", int64(12), sqlmock.AnyArg()).
		WillReturnRows(loanRows().AddRow("12", "car", "ACC-1", 5000.0, 12, 4.5, 0.0, "2025-01-01", "2026-01-01", 430.0, "2026-04-01", 3000.0, "EUR", "approved"))

	resp, err := server.GetLoanByNumber(context.Background(), &bankpb.GetLoanByNumberRequest{ClientEmail: "a@b.rs", LoanNumber: "12"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.LoanNumber != "12" || resp.LoanType != "car" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateLoanRequestSuccessAndCurrencyMismatch(t *testing.T) {
	server, mock := newGormTestServer(t)

	accountRows := sqlmock.NewRows([]string{
		"id", "number", "name", "owner", "balance", "created_by", "created_at", "valid_until", "currency", "active", "owner_type", "account_type", "maintainance_cost", "daily_limit", "monthly_limit", "daily_expenditure", "monthly_expenditure",
	}).AddRow(int64(10), "ACC-1", "acc", int64(5), int64(0), int64(1), time.Now(), time.Now().AddDate(1, 0, 0), "RSD", true, "personal", "checking", int64(0), int64(0), int64(0), int64(0), int64(0))

	mock.ExpectQuery(regexp.QuoteMeta(`FROM "accounts" JOIN clients ON clients.id = accounts.owner WHERE clients.email = $1 AND accounts.number = $2 ORDER BY "accounts"."id" LIMIT $3`)).
		WithArgs("client@test.com", "ACC-1", 1).
		WillReturnRows(accountRows)

	currencyRows := sqlmock.NewRows([]string{"id", "label", "name", "symbol", "countries", "description", "active"}).
		AddRow(int64(1), "RSD", "Dinar", "RSD", "RS", "Serbian dinar", true)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "currencies" WHERE label = $1 ORDER BY "currencies"."id" LIMIT $2`)).
		WithArgs("RSD", 1).
		WillReturnRows(currencyRows)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "loan_request" ("type","currency_id","amount","repayment_period","account_id","status","submission_date") VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING "id"`)).
		WithArgs("cash", int64(1), 10000.0, int64(12), int64(10), "pending", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(101)))
	mock.ExpectCommit()

	_, err := server.CreateLoanRequest(context.Background(), &bankpb.CreateLoanRequestRequest{
		ClientEmail:     "client@test.com",
		AccountNumber:   "ACC-1",
		Currency:        "RSD",
		Amount:          10000,
		RepaymentPeriod: 12,
		LoanType:        "GOTOVINSKI",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	accountRowsMismatch := sqlmock.NewRows([]string{
		"id", "number", "name", "owner", "balance", "created_by", "created_at", "valid_until", "currency", "active", "owner_type", "account_type", "maintainance_cost", "daily_limit", "monthly_limit", "daily_expenditure", "monthly_expenditure",
	}).AddRow(int64(11), "ACC-2", "acc", int64(5), int64(0), int64(1), time.Now(), time.Now().AddDate(1, 0, 0), "EUR", true, "personal", "checking", int64(0), int64(0), int64(0), int64(0), int64(0))

	mock.ExpectQuery(regexp.QuoteMeta(`FROM "accounts" JOIN clients ON clients.id = accounts.owner WHERE clients.email = $1 AND accounts.number = $2 ORDER BY "accounts"."id" LIMIT $3`)).
		WithArgs("client@test.com", "ACC-2", 1).
		WillReturnRows(accountRowsMismatch)

	_, err = server.CreateLoanRequest(context.Background(), &bankpb.CreateLoanRequestRequest{
		ClientEmail:     "client@test.com",
		AccountNumber:   "ACC-2",
		Currency:        "RSD",
		Amount:          10000,
		RepaymentPeriod: 12,
		LoanType:        "GOTOVINSKI",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for currency mismatch, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateLoanRequestValidation(t *testing.T) {
	server, mock := newGormTestServer(t)

	_, err := server.CreateLoanRequest(context.Background(), &bankpb.CreateLoanRequestRequest{ClientEmail: ""})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", status.Code(err))
	}

	_, err = server.CreateLoanRequest(context.Background(), &bankpb.CreateLoanRequestRequest{ClientEmail: "a@b.rs", Currency: "RSD", Amount: 1, RepaymentPeriod: 1, LoanType: "GOTOVINSKI"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for account number, got %v", status.Code(err))
	}

	_, err = server.CreateLoanRequest(context.Background(), &bankpb.CreateLoanRequestRequest{ClientEmail: "a@b.rs", AccountNumber: "A", Amount: 1, RepaymentPeriod: 1, LoanType: "GOTOVINSKI"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for currency, got %v", status.Code(err))
	}

	_, err = server.CreateLoanRequest(context.Background(), &bankpb.CreateLoanRequestRequest{ClientEmail: "a@b.rs", AccountNumber: "A", Currency: "RSD", Amount: 0, RepaymentPeriod: 1, LoanType: "GOTOVINSKI"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for amount, got %v", status.Code(err))
	}

	_, err = server.CreateLoanRequest(context.Background(), &bankpb.CreateLoanRequestRequest{ClientEmail: "a@b.rs", AccountNumber: "A", Currency: "RSD", Amount: 1, RepaymentPeriod: 0, LoanType: "GOTOVINSKI"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for repayment period, got %v", status.Code(err))
	}

	_, err = server.CreateLoanRequest(context.Background(), &bankpb.CreateLoanRequestRequest{ClientEmail: "a@b.rs", AccountNumber: "A", Currency: "RSD", Amount: 1, RepaymentPeriod: 1, LoanType: "bad"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for loan type, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateLoanRequestAccountAndCurrencyErrors(t *testing.T) {
	server, mock := newGormTestServer(t)

	mock.ExpectQuery(regexp.QuoteMeta(`FROM "accounts" JOIN clients ON clients.id = accounts.owner WHERE clients.email = $1 AND accounts.number = $2 ORDER BY "accounts"."id" LIMIT $3`)).
		WithArgs("a@b.rs", "ACC-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "number", "name", "owner", "balance", "created_by", "created_at", "valid_until", "currency", "active", "owner_type", "account_type", "maintainance_cost", "daily_limit", "monthly_limit", "daily_expenditure", "monthly_expenditure"}))

	_, err := server.CreateLoanRequest(context.Background(), &bankpb.CreateLoanRequestRequest{
		ClientEmail:     "a@b.rs",
		AccountNumber:   "ACC-1",
		Currency:        "RSD",
		Amount:          10,
		RepaymentPeriod: 1,
		LoanType:        "GOTOVINSKI",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for missing account, got %v", status.Code(err))
	}

	accountRows := sqlmock.NewRows([]string{"id", "number", "name", "owner", "balance", "created_by", "created_at", "valid_until", "currency", "active", "owner_type", "account_type", "maintainance_cost", "daily_limit", "monthly_limit", "daily_expenditure", "monthly_expenditure"}).
		AddRow(int64(1), "ACC-1", "acc", int64(1), int64(0), int64(1), time.Now(), time.Now().AddDate(1, 0, 0), "RSD", true, "personal", "checking", int64(0), int64(0), int64(0), int64(0), int64(0))
	mock.ExpectQuery(regexp.QuoteMeta(`FROM "accounts" JOIN clients ON clients.id = accounts.owner WHERE clients.email = $1 AND accounts.number = $2 ORDER BY "accounts"."id" LIMIT $3`)).
		WithArgs("a@b.rs", "ACC-1", 1).
		WillReturnRows(accountRows)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "currencies" WHERE label = $1 ORDER BY "currencies"."id" LIMIT $2`)).
		WithArgs("RSD", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "label", "name", "symbol", "countries", "description", "active"}))

	_, err = server.CreateLoanRequest(context.Background(), &bankpb.CreateLoanRequestRequest{
		ClientEmail:     "a@b.rs",
		AccountNumber:   "ACC-1",
		Currency:        "RSD",
		Amount:          10,
		RepaymentPeriod: 1,
		LoanType:        "GOTOVINSKI",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for unsupported currency, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
