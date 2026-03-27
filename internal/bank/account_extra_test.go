package bank

import (
	"context"
	"os"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"google.golang.org/grpc/metadata"
)

func TestUpdateAccountName(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	userSrv := &testUserServer{isEmployee: false, clientID: 10, clientMail: "user@mail.com"}
	addr, stop := startUserMock(userSrv)
	defer stop()
	_ = os.Setenv("USER_SERVICE_ADDR", addr)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-email", "user@mail.com"))

	req := &bankpb.UpdateAccountNameRequest{AccountNumber: "111", Name: "Novi Naziv"}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "accounts" WHERE "accounts"."number" = $1`)).
		WithArgs("111", 1).
		WillReturnRows(sqlmock.NewRows([]string{"number", "owner"}).AddRow("111", 10))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "accounts" WHERE owner = $1 AND name = $2 AND number <> $3`)).
		WithArgs(int64(10), "Novi Naziv", "111").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "accounts" SET "name"=$1 WHERE number = $2`)).
		WithArgs("Novi Naziv", "111").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	_, err := server.UpdateAccountName(ctx, req)
	if err != nil {
		t.Fatalf("unexpected fail: %v", err)
	}
}

func TestUpdateAccountLimits(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	userSrv := &testUserServer{isEmployee: false, clientID: 10, clientMail: "user@mail.com"}
	addr, stop := startUserMock(userSrv)
	defer stop()
	_ = os.Setenv("USER_SERVICE_ADDR", addr)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-email", "user@mail.com"))

	dl := int64(1000)
	ml := int64(5000)
	req := &bankpb.UpdateAccountLimitsRequest{AccountNumber: "111", DailyLimit: &dl, MonthlyLimit: &ml}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "accounts" WHERE "accounts"."number" = $1`)).
		WithArgs("111", 1).
		WillReturnRows(sqlmock.NewRows([]string{"number", "owner"}).AddRow("111", 10))

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "accounts" SET "daily_limit"=$1,"monthly_limit"=$2 WHERE number = $3`)).
		WithArgs(1000, 5000, "111").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	_, err := server.UpdateAccountLimits(ctx, req)
	if err != nil {
		t.Fatalf("unexpected fail: %v", err)
	}
}
