package test

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetClientsWithoutFiltersReturnsExpectedRows(t *testing.T) {
	gormTestServer, mock, db := NewGormTestServer(t)
	defer func() { _ = db.Close() }()

	date1 := time.Date(1990, 5, 20, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(1992, 6, 21, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "clients`)).
		WillReturnRows(sqlmockClientRows().
			AddRow(uint64(1), "Petar", "Petrovic", date1, "M", "petar@primer.raf", "+381645555555", "Njegoseva 25").
			AddRow(uint64(2), "Jana", "Janic", date2, "F", "jana@primer.raf", "+381645555556", "Bulevar 1"))

	resp, err := gormTestServer.GetClients(context.Background(), &userpb.GetClientsRequest{})
	if err != nil {
		t.Fatalf("GetClients returned error: %v", err)
	}
	if len(resp.Clients) != 2 {
		t.Fatalf("expected 2 clients, got %d", len(resp.Clients))
	}
	if resp.Clients[0].Email != "petar@primer.raf" {
		t.Fatalf("unexpected first client email: %s", resp.Clients[0].Email)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetClientsWithFiltersBuildsExpectedSQL(t *testing.T) {
	gormTestServer, mock, db := NewGormTestServer(t)
	defer func() { _ = db.Close() }()

	date := time.Date(1990, 5, 20, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "clients" WHERE email = $1 AND first_name ILIKE $2 AND last_name ILIKE $3`)).
		WithArgs("petar@primer.raf", "Petar", "Petrovic").
		WillReturnRows(sqlmockClientRows().
			AddRow(uint64(1), "Petar", "Petrovic", date, "M", "petar@primer.raf", "+381645555555", "Njegoseva 25"))

	resp, err := gormTestServer.GetClients(context.Background(), &userpb.GetClientsRequest{
		FirstName: "Petar",
		LastName:  "Petrovic",
		Email:     "petar@primer.raf",
	})
	if err != nil {
		t.Fatalf("GetClients returned error: %v", err)
	}
	if len(resp.Clients) != 1 {
		t.Fatalf("expected 1 client, got %d", len(resp.Clients))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestUpdateClientSuccess(t *testing.T) {
	gormTestServer, mock, db := NewGormTestServer(t)
	defer func() { _ = db.Close() }()

	date := time.Date(1990, 5, 20, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "clients" WHERE "clients"."id" = $1 ORDER BY "clients"."id" LIMIT $2`)).
		WithArgs(int64(1), int64(1)).
		WillReturnRows(sqlmockClientRows().
			AddRow(uint64(1), "Petar", "Petrovic", date, "M", "petar@primer.raf", "+381645555555", "Njegoseva 25"))
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "clients" SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	resp, err := gormTestServer.UpdateClient(context.Background(), &userpb.UpdateClientRequest{
		Id:          1,
		Email:       "petar.novi@primer.raf",
		PhoneNumber: "+381600000000",
	})
	if err != nil {
		t.Fatalf("UpdateClient returned error: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected valid=true, got false")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestUpdateClientNotFound(t *testing.T) {
	gormTestServer, mock, db := NewGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "clients" WHERE "clients"."id" = $1 ORDER BY "clients"."id" LIMIT $2`)).
		WithArgs(int64(404), int64(1)).
		WillReturnError(sql.ErrNoRows)

	_, err := gormTestServer.UpdateClient(context.Background(), &userpb.UpdateClientRequest{
		Id:    404,
		Email: "missing@primer.raf",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestUpdateClientDuplicateEmailReturnsAlreadyExists(t *testing.T) {
	gormTestServer, mock, db := NewGormTestServer(t)
	defer func() { _ = db.Close() }()

	date := time.Date(1990, 5, 20, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "clients" WHERE "clients"."id" = $1 ORDER BY "clients"."id" LIMIT $2`)).
		WithArgs(int64(1), int64(1)).
		WillReturnRows(sqlmockClientRows().
			AddRow(uint64(1), "Petar", "Petrovic", date, "M", "petar@primer.raf", "+381645555555", "Njegoseva 25"))
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "clients" SET`).
		WillReturnError(&pgconn.PgError{Code: "23505"})
	mock.ExpectRollback()

	_, err := gormTestServer.UpdateClient(context.Background(), &userpb.UpdateClientRequest{
		Id:    1,
		Email: "admin@banka.raf",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestUpdateClientInvalidGenderReturnsInvalidArgument(t *testing.T) {
	gormTestServer, mock, db := NewGormTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := gormTestServer.UpdateClient(context.Background(), &userpb.UpdateClientRequest{
		Id:     1,
		Gender: "X",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func sqlmockClientRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "first_name", "last_name", "date_of_birth", "gender", "email", "phone_number", "address"})
}
