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

func TestGetCardsSuccess(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	userSrv := &testUserServer{isEmployee: true}
	addr, stop := startUserMock(userSrv)
	defer stop()
	_ = os.Setenv("USER_SERVICE_ADDR", addr)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-email", "emp@banka.rs"))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "employees" WHERE email = $1`)).
		WithArgs("emp@banka.rs").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "cards" ORDER BY id DESC`)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "number"}).AddRow(1, "123"))

	resp, err := server.GetCards(ctx, &bankpb.GetCardsRequest{})
	if err != nil {
		t.Fatalf("unexpected fail: %v", err)
	}
	if len(resp.Cards) != 1 {
		t.Fatalf("unexpected len")
	}
}

func TestGetCardsClient(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	userSrv := &testUserServer{isEmployee: false, clientID: 10, clientMail: "user@mail.com"}
	addr, stop := startUserMock(userSrv)
	defer stop()
	_ = os.Setenv("USER_SERVICE_ADDR", addr)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-email", "user@mail.com"))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT count(*) FROM "employees" WHERE email = $1`)).
		WithArgs("user@mail.com").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT "id" FROM "clients" WHERE email = $1 LIMIT $2`)).
		WithArgs("user@mail.com", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT "cards"."id","cards"."number","cards"."type","cards"."brand","cards"."creation_date","cards"."valid_until","cards"."account_number","cards"."cvv","cards"."card_limit","cards"."status" FROM "cards" JOIN accounts ON accounts.number = cards.account_number WHERE accounts.owner = $1 ORDER BY cards.id DESC`)).
		WithArgs(int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "number"}).AddRow(1, "123"))

	resp, err := server.GetCards(ctx, &bankpb.GetCardsRequest{})
	if err != nil {
		t.Fatalf("unexpected fail: %v", err)
	}
	if len(resp.Cards) != 1 {
		t.Fatalf("unexpected len")
	}
}
