package bank

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// GetLoanRequests
// ---------------------------------------------------------------------------

func TestGetLoanRequests_Success(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	rows := sqlmock.NewRows([]string{
		"id", "loan_type", "amount", "currency", "purpose", "salary",
		"employment_status", "employment_period", "phone_number",
		"repayment_period", "account_number", "status", "interest_rate_type",
		"submission_date",
	}).AddRow(
		int64(1), "cash", int64(10_000_00), "RSD", "home repair", int64(50_000_00),
		"full_time", int64(24), "0611234567",
		int32(12), "12345678901234567890", "pending", "fixed",
		"2026-03-01T10:00:00",
	)

	mock.ExpectQuery(`SELECT`).WillReturnRows(rows)

	resp, err := server.GetLoanRequests(context.Background(), &bankpb.GetLoanRequestsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.LoanRequests) != 1 {
		t.Fatalf("expected 1 loan request, got %d", len(resp.LoanRequests))
	}
	lr := resp.LoanRequests[0]
	if lr.Id != 1 || lr.LoanType != "cash" || lr.Purpose != "home repair" {
		t.Fatalf("unexpected loan request: %+v", lr)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetLoanRequests_Empty(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	rows := sqlmock.NewRows([]string{
		"id", "loan_type", "amount", "currency", "purpose", "salary",
		"employment_status", "employment_period", "phone_number",
		"repayment_period", "account_number", "status", "interest_rate_type",
		"submission_date",
	})

	mock.ExpectQuery(`SELECT`).WillReturnRows(rows)

	resp, err := server.GetLoanRequests(context.Background(), &bankpb.GetLoanRequestsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.LoanRequests) != 0 {
		t.Fatalf("expected 0 loan requests, got %d", len(resp.LoanRequests))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetLoanRequests_DBError(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT`).WillReturnError(fmt.Errorf("db error"))

	_, err := server.GetLoanRequests(context.Background(), &bankpb.GetLoanRequestsRequest{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ApproveLoanRequest
// ---------------------------------------------------------------------------

func TestApproveLoanRequest_Success(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()

	// getLoanRequestByID
	loanReqRows := sqlmock.NewRows([]string{
		"id", "type", "currency_id", "amount", "repayment_period",
		"account_id", "status", "submission_date",
		"purpose", "salary", "employment_status", "employment_period",
		"phone_number", "interest_rate_type",
	}).AddRow(
		int64(1), "cash", int64(1), int64(10_000_00), int64(12),
		int64(1), "pending", now,
		"repair", int64(50_000_00), "full_time", int64(24),
		"0611234567", "fixed",
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "loan_request" WHERE "loan_request"."id" = $1`)).
		WithArgs(int64(1), 1).
		WillReturnRows(loanReqRows)

	// Fetch account
	accountRows := sqlmock.NewRows([]string{
		"id", "number", "name", "owner", "balance", "created_by", "created_at",
		"valid_until", "currency", "active", "owner_type", "account_type",
		"maintainance_cost", "daily_limit", "monthly_limit", "daily_expenditure",
		"monthly_expenditure",
	}).AddRow(
		int64(1), "12345678901234567890", "Main", int64(1), int64(0), int64(1), now,
		now.AddDate(3, 0, 0), "RSD", true, "personal", "checking",
		int64(0), int64(0), int64(0), int64(0), int64(0),
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "accounts" WHERE "accounts"."id" = $1`)).
		WithArgs(int64(1), 1).
		WillReturnRows(accountRows)

	// Fetch currency
	currencyRows := sqlmock.NewRows([]string{
		"id", "label", "name", "symbol", "countries", "description", "active",
	}).AddRow(int64(1), "RSD", "Serbian Dinar", "din", "Serbia", "Serbian Dinar", true)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "currencies" WHERE "currencies"."id" = $1`)).
		WithArgs(int64(1), 1).
		WillReturnRows(currencyRows)

	// Transaction: create loan, create installment, update loan request
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "loans"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery(`INSERT INTO "loan_installment"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectExec(`UPDATE "loan_request"`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	resp, err := server.ApproveLoanRequest(context.Background(), &bankpb.ApproveLoanRequestRequest{Id: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestApproveLoanRequest_NotFound(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "loan_request" WHERE "loan_request"."id" = $1`)).
		WithArgs(int64(999), 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "type", "currency_id", "amount", "repayment_period",
			"account_id", "status", "submission_date",
			"purpose", "salary", "employment_status", "employment_period",
			"phone_number", "interest_rate_type",
		}))

	_, err := server.ApproveLoanRequest(context.Background(), &bankpb.ApproveLoanRequestRequest{Id: 999})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestApproveLoanRequest_AlreadyProcessed(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()
	loanReqRows := sqlmock.NewRows([]string{
		"id", "type", "currency_id", "amount", "repayment_period",
		"account_id", "status", "submission_date",
		"purpose", "salary", "employment_status", "employment_period",
		"phone_number", "interest_rate_type",
	}).AddRow(
		int64(1), "cash", int64(1), int64(10_000_00), int64(12),
		int64(1), "approved", now,
		"", int64(0), "", int64(0), "", "fixed",
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "loan_request" WHERE "loan_request"."id" = $1`)).
		WithArgs(int64(1), 1).
		WillReturnRows(loanReqRows)

	_, err := server.ApproveLoanRequest(context.Background(), &bankpb.ApproveLoanRequestRequest{Id: 1})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestApproveLoanRequest_InvalidID(t *testing.T) {
	server, _, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.ApproveLoanRequest(context.Background(), &bankpb.ApproveLoanRequestRequest{Id: 0})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// RejectLoanRequest
// ---------------------------------------------------------------------------

func TestRejectLoanRequest_Success(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()
	loanReqRows := sqlmock.NewRows([]string{
		"id", "type", "currency_id", "amount", "repayment_period",
		"account_id", "status", "submission_date",
		"purpose", "salary", "employment_status", "employment_period",
		"phone_number", "interest_rate_type",
	}).AddRow(
		int64(1), "cash", int64(1), int64(10_000_00), int64(12),
		int64(1), "pending", now,
		"", int64(0), "", int64(0), "", "fixed",
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "loan_request" WHERE "loan_request"."id" = $1`)).
		WithArgs(int64(1), 1).
		WillReturnRows(loanReqRows)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "loan_request"`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	resp, err := server.RejectLoanRequest(context.Background(), &bankpb.RejectLoanRequestRequest{Id: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRejectLoanRequest_NotFound(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "loan_request" WHERE "loan_request"."id" = $1`)).
		WithArgs(int64(999), 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "type", "currency_id", "amount", "repayment_period",
			"account_id", "status", "submission_date",
			"purpose", "salary", "employment_status", "employment_period",
			"phone_number", "interest_rate_type",
		}))

	_, err := server.RejectLoanRequest(context.Background(), &bankpb.RejectLoanRequestRequest{Id: 999})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRejectLoanRequest_AlreadyProcessed(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()
	loanReqRows := sqlmock.NewRows([]string{
		"id", "type", "currency_id", "amount", "repayment_period",
		"account_id", "status", "submission_date",
		"purpose", "salary", "employment_status", "employment_period",
		"phone_number", "interest_rate_type",
	}).AddRow(
		int64(1), "cash", int64(1), int64(10_000_00), int64(12),
		int64(1), "rejected", now,
		"", int64(0), "", int64(0), "", "fixed",
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "loan_request" WHERE "loan_request"."id" = $1`)).
		WithArgs(int64(1), 1).
		WillReturnRows(loanReqRows)

	_, err := server.RejectLoanRequest(context.Background(), &bankpb.RejectLoanRequestRequest{Id: 1})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetAllLoans
// ---------------------------------------------------------------------------

func TestGetAllLoans_Success(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	rows := sqlmock.NewRows([]string{
		"loan_number", "loan_type", "account_number", "loan_amount",
		"repayment_period", "nominal_rate", "effective_rate",
		"agreement_date", "maturity_date", "next_installment_amount",
		"next_installment_date", "remaining_debt", "currency", "status",
	}).AddRow(
		"1", "cash", "12345678901234567890", int64(10_000_00),
		int32(12), 5.0, 0.0,
		"2026-03-01", "2027-03-01", int64(83_333),
		"2026-04-01", int64(10_000_00), "RSD", "approved",
	)

	mock.ExpectQuery(`SELECT`).WillReturnRows(rows)

	resp, err := server.GetAllLoans(context.Background(), &bankpb.GetAllLoansRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Loans) != 1 {
		t.Fatalf("expected 1 loan, got %d", len(resp.Loans))
	}
	if resp.Loans[0].LoanNumber != "1" || resp.Loans[0].LoanType != "cash" {
		t.Fatalf("unexpected loan: %+v", resp.Loans[0])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetAllLoans_Empty(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	rows := sqlmock.NewRows([]string{
		"loan_number", "loan_type", "account_number", "loan_amount",
		"repayment_period", "nominal_rate", "effective_rate",
		"agreement_date", "maturity_date", "next_installment_amount",
		"next_installment_date", "remaining_debt", "currency", "status",
	})

	mock.ExpectQuery(`SELECT`).WillReturnRows(rows)

	resp, err := server.GetAllLoans(context.Background(), &bankpb.GetAllLoansRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Loans) != 0 {
		t.Fatalf("expected 0 loans, got %d", len(resp.Loans))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetAllLoans_DBError(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT`).WillReturnError(fmt.Errorf("db error"))

	_, err := server.GetAllLoans(context.Background(), &bankpb.GetAllLoansRequest{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
