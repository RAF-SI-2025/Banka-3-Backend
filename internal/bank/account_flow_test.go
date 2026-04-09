package bank

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type timeArgument struct{}

func (timeArgument) Match(v driver.Value) bool {
	_, ok := v.(time.Time)
	return ok
}

type testNotificationServer struct {
	notificationpb.UnimplementedNotificationServiceServer
}

func TestCreateAccountSuccess(t *testing.T) {
	notificationServer := &testNotificationServer{}
	notifAddr, notifStop := startNotificationTestServer(t, notificationServer)
	defer notifStop()
	t.Setenv("NOTIFICATION_GRPC_ADDR", notifAddr)

	userServer := &testUserServer{}
	userAddr, userStop := startUserTestServer(t, userServer)
	defer userStop()
	t.Setenv("USER_GRPC_ADDR", userAddr)

	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	createdAt := time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC)
	validUntil := time.Date(2029, 3, 19, 0, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1)`)).
		WithArgs(int64(3)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM currencies WHERE label = $1)`)).
		WithArgs("EUR").
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM accounts WHERE number = $1)`)).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmockBoolRow(false))
	mock.ExpectQuery("INSERT INTO accounts").
		WithArgs(
			sqlmock.AnyArg(),
			"Licni racun",
			int64(1),
			int64(0),
			int64(3),
			timeArgument{},
			"EUR",
			false,
			"personal",
			"checking",
			int64(250),
			nil,
			nil,
			int64(0),
			int64(0),
		).
		WillReturnRows(sqlmockAccountRows().AddRow(
			int64(12),
			"12345678901234567890",
			"Licni racun",
			int64(1),
			int64(0),
			int64(3),
			createdAt,
			validUntil,
			"EUR",
			false,
			"personal",
			"checking",
			int64(250),
			nil,
			nil,
			int64(0),
			int64(0),
		))
	mock.ExpectCommit()

	resp, err := server.CreateAccount(context.Background(), &bankpb.CreateAccountRequest{
		Name:             "Licni racun",
		Owner:            1,
		Currency:         "EUR",
		OwnerType:        "personal",
		AccountType:      "checking",
		MaintainanceCost: 250,
		CreatedBy:        3,
	})
	if err != nil {
		t.Fatalf("CreateAccount returned error: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected valid response")
	}
	if resp.AccountNumber != "12345678901234567890" {
		t.Fatalf("unexpected account number: %s", resp.AccountNumber)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateAccountInvalidOwnerType(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.CreateAccount(context.Background(), &bankpb.CreateAccountRequest{
		Name:             "Racun",
		Owner:            1,
		Currency:         "EUR",
		OwnerType:        "invalid",
		AccountType:      "checking",
		MaintainanceCost: 100,
		CreatedBy:        1,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateAccountInvalidAccountType(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.CreateAccount(context.Background(), &bankpb.CreateAccountRequest{
		Name:             "Racun",
		Owner:            1,
		Currency:         "EUR",
		OwnerType:        "personal",
		AccountType:      "invalid",
		MaintainanceCost: 100,
		CreatedBy:        1,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateAccountNegativeMaintainanceCost(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.CreateAccount(context.Background(), &bankpb.CreateAccountRequest{
		Name:             "Racun",
		Owner:            1,
		Currency:         "EUR",
		OwnerType:        "personal",
		AccountType:      "checking",
		MaintainanceCost: -1,
		CreatedBy:        1,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateAccountOwnerNotFound(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(44)).
		WillReturnRows(sqlmockBoolRow(false))

	_, err := server.CreateAccount(context.Background(), &bankpb.CreateAccountRequest{
		Name:             "Racun",
		Owner:            44,
		Currency:         "EUR",
		OwnerType:        "personal",
		AccountType:      "checking",
		MaintainanceCost: 100,
		CreatedBy:        1,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateAccountCreatorNotFound(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1)`)).
		WithArgs(int64(77)).
		WillReturnRows(sqlmockBoolRow(false))

	_, err := server.CreateAccount(context.Background(), &bankpb.CreateAccountRequest{
		Name:             "Racun",
		Owner:            1,
		Currency:         "EUR",
		OwnerType:        "personal",
		AccountType:      "checking",
		MaintainanceCost: 100,
		CreatedBy:        77,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateAccountCurrencyNotFound(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1)`)).
		WithArgs(int64(3)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM currencies WHERE label = $1)`)).
		WithArgs("USD").
		WillReturnRows(sqlmockBoolRow(false))

	_, err := server.CreateAccount(context.Background(), &bankpb.CreateAccountRequest{
		Name:             "Racun",
		Owner:            1,
		Currency:         "USD",
		OwnerType:        "personal",
		AccountType:      "checking",
		MaintainanceCost: 100,
		CreatedBy:        3,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateAccountDefaultValidUntilAndZeroLimitsBecomeNull(t *testing.T) {
	notificationServer := &testNotificationServer{}
	notifAddr, notifStop := startNotificationTestServer(t, notificationServer)
	defer notifStop()
	t.Setenv("NOTIFICATION_GRPC_ADDR", notifAddr)

	userServer := &testUserServer{}
	userAddr, userStop := startUserTestServer(t, userServer)
	defer userStop()
	t.Setenv("USER_GRPC_ADDR", userAddr)

	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	createdAt := time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC)
	validUntil := time.Date(2029, 3, 19, 0, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM currencies WHERE label = $1)`)).
		WithArgs("EUR").
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM accounts WHERE number = $1)`)).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmockBoolRow(false))
	mock.ExpectQuery("INSERT INTO accounts").
		WithArgs(
			sqlmock.AnyArg(),
			"Stednja",
			int64(1),
			int64(0),
			int64(1),
			timeArgument{},
			"EUR",
			false,
			"business",
			"foreign",
			int64(0),
			nil,
			nil,
			int64(0),
			int64(0),
		).
		WillReturnRows(sqlmockAccountRows().AddRow(
			int64(13),
			"99999999999999999999",
			"Stednja",
			int64(1),
			int64(0),
			int64(1),
			createdAt,
			validUntil,
			"EUR",
			false,
			"business",
			"foreign",
			int64(0),
			nil,
			nil,
			int64(0),
			int64(0),
		))
	mock.ExpectCommit()

	resp, err := server.CreateAccount(context.Background(), &bankpb.CreateAccountRequest{
		Name:             "Stednja",
		Owner:            1,
		Currency:         "EUR",
		OwnerType:        "business",
		AccountType:      "foreign",
		MaintainanceCost: 0,
		CreatedBy:        1,
	})
	if err != nil {
		t.Fatalf("CreateAccount returned error: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected valid response")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateAccountNumberCollisionRetryPath(t *testing.T) {
	notificationServer := &testNotificationServer{}
	notifAddr, notifStop := startNotificationTestServer(t, notificationServer)
	defer notifStop()
	t.Setenv("NOTIFICATION_GRPC_ADDR", notifAddr)

	userServer := &testUserServer{}
	userAddr, userStop := startUserTestServer(t, userServer)
	defer userStop()
	t.Setenv("USER_GRPC_ADDR", userAddr)

	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	createdAt := time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC)
	validUntil := time.Date(2030, 3, 19, 0, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM currencies WHERE label = $1)`)).
		WithArgs("EUR").
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM accounts WHERE number = $1)`)).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmockBoolRow(false))
	mock.ExpectQuery("INSERT INTO accounts").
		WithArgs(
			sqlmock.AnyArg(),
			"Retry racun",
			int64(1),
			int64(0),
			int64(1),
			timeArgument{},
			"EUR",
			false,
			"personal",
			"checking",
			int64(10),
			nil,
			nil,
			int64(0),
			int64(0),
		).
		WillReturnError(&pgconn.PgError{Code: "23505"})
	mock.ExpectRollback()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM currencies WHERE label = $1)`)).
		WithArgs("EUR").
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM accounts WHERE number = $1)`)).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmockBoolRow(false))
	mock.ExpectQuery("INSERT INTO accounts").
		WithArgs(
			sqlmock.AnyArg(),
			"Retry racun",
			int64(1),
			int64(0),
			int64(1),
			timeArgument{},
			"EUR",
			false,
			"personal",
			"checking",
			int64(10),
			nil,
			nil,
			int64(0),
			int64(0),
		).
		WillReturnRows(sqlmockAccountRows().AddRow(
			int64(99),
			"55555555555555555555",
			"Retry račun",
			int64(1),
			int64(0),
			int64(1),
			createdAt,
			validUntil,
			"EUR",
			false,
			"personal",
			"checking",
			int64(10),
			nil,
			nil,
			int64(0),
			int64(0),
		))
	mock.ExpectCommit()

	resp, err := server.CreateAccount(context.Background(), &bankpb.CreateAccountRequest{
		Name:             "Retry racun",
		Owner:            1,
		Currency:         "EUR",
		OwnerType:        "personal",
		AccountType:      "checking",
		MaintainanceCost: 10,
		CreatedBy:        1,
	})
	if err != nil {
		t.Fatalf("CreateAccount returned error: %v", err)
	}
	if resp.AccountNumber != "55555555555555555555" {
		t.Fatalf("unexpected account number: %s", resp.AccountNumber)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
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
