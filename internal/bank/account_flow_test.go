package bank

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type timeArgument struct{}

func (timeArgument) Match(v driver.Value) bool {
	_, ok := v.(time.Time)
	return ok
}

func TestCreateAccountSuccess(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	createdAt := time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC)
	validUntil := time.Date(2029, 3, 19, 0, 0, 0, 0, time.UTC)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"user-email", "test@example.com",
		"employee-id", "3",
	))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM employees`)).
		WithArgs("test@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(3)))

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "accounts"`)).
		WithArgs(
			sqlmock.AnyArg(),
			"checking-personal",
			int64(1),
			int64(100000),
			int64(3),
			timeArgument{},
			"EUR",
			"personal",
			"checking",
			int64(0),
			int64(500),
			int64(5000),
			nil, nil, nil, nil, nil,
		).
		WillReturnRows(sqlmockAccountRows().AddRow(
			int64(12),
			"12345678901234567890",
			"checking-personal",
			int64(1),
			int64(100000),
			int64(3),
			createdAt,
			validUntil,
			"EUR",
			true,
			"personal",
			"checking",
			int64(0),
			int64(500),
			int64(5000),
			int64(0),
			int64(0),
		))

	_, _ = server.CreateAccount(ctx, &bankpb.CreateAccountRequest{
		ClientId:       1,
		Currency:       "EUR",
		Subtype:        "personal",
		AccountType:    "checking",
		InitialBalance: 1000,
		DailyLimit:     500,
		MonthlyLimit:   5000,
	})

}

func TestCreateAccountInvalidAccountType(t *testing.T) {
	server, _, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.CreateAccount(context.Background(), &bankpb.CreateAccountRequest{
		ClientId:    1,
		Currency:    "EUR",
		Subtype:     "personal",
		AccountType: "invalid",
	})

	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestCreateAccountMissingMetadata(t *testing.T) {
	server, _, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.CreateAccount(context.Background(), &bankpb.CreateAccountRequest{
		ClientId:    1,
		Currency:    "EUR",
		Subtype:     "personal",
		AccountType: "checking",
	})

	if err == nil {
		t.Fatalf("expected error due to missing metadata")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", status.Code(err))
	}
}

func TestCreateAccountCollisionRetryPath(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	createdAt := time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC)
	validUntil := time.Date(2030, 3, 19, 0, 0, 0, 0, time.UTC)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"user-email", "test@example.com",
		"employee-id", "1",
	))

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "accounts"`)).
		WillReturnError(&pgconn.PgError{Code: "23505"})

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "accounts"`)).
		WillReturnRows(sqlmockAccountRows().AddRow(
			int64(99),
			"55555555555555555555",
			"checking-personal",
			int64(1),
			int64(0),
			int64(1),
			createdAt,
			validUntil,
			"EUR",
			true,
			"personal",
			"checking",
			int64(0),
			int64(0),
			int64(0),
			int64(0),
			int64(0),
		))

	_, _ = server.CreateAccount(ctx, &bankpb.CreateAccountRequest{
		ClientId:    1,
		Currency:    "EUR",
		Subtype:     "personal",
		AccountType: "checking",
	})

}

func sqlmockAccountRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id",
		"number",
		"name",
		"owner",
		"balance",
		"created_by",
		"created_at",
		"valid_until",
		"currency",
		"active",
		"owner_type",
		"account_type",
		"maintainance_cost",
		"daily_limit",
		"monthly_limit",
		"daily_expenditure",
		"monthly_expenditure",
	})
}
