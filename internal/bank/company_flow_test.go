package bank

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCreateCompanySuccess(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM activity_codes WHERE id = $1)`)).
		WithArgs(int64(2)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery("INSERT INTO companies").
		WithArgs(int64(12345), "ACME", int64(999), int64(2), "Main street", int64(1)).
		WillReturnRows(sqlmockCompanyRows().AddRow(int64(10), int64(12345), "ACME", int64(999), int64(2), "Main street", int64(1)))
	mock.ExpectCommit()

	resp, err := server.CreateCompany(context.Background(), &bankpb.CreateCompanyRequest{
		RegisteredId:   12345,
		Name:           "ACME",
		TaxCode:        999,
		ActivityCodeId: 2,
		Address:        "Main street",
		OwnerId:        1,
	})
	if err != nil {
		t.Fatalf("CreateCompany returned error: %v", err)
	}
	if resp.Company == nil {
		t.Fatalf("expected company in response")
	}
	if resp.Company.Id != 10 || resp.Company.RegisteredId != 12345 {
		t.Fatalf("unexpected company response: %+v", resp.Company)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateCompanyDuplicateRegisteredID(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery("INSERT INTO companies").
		WithArgs(int64(12345), "ACME", int64(999), "Main street", int64(1)).
		WillReturnError(&pgconn.PgError{Code: "23505"})

	_, err := server.CreateCompany(context.Background(), &bankpb.CreateCompanyRequest{
		RegisteredId: 12345,
		Name:         "ACME",
		TaxCode:      999,
		Address:      "Main street",
		OwnerId:      1,
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

func TestCreateCompanyOwnerNotFound(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(77)).
		WillReturnRows(sqlmockBoolRow(false))

	_, err := server.CreateCompany(context.Background(), &bankpb.CreateCompanyRequest{
		RegisteredId: 12345,
		Name:         "ACME",
		TaxCode:      999,
		Address:      "Main street",
		OwnerId:      77,
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

func TestCreateCompanyActivityCodeNotFound(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM activity_codes WHERE id = $1)`)).
		WithArgs(int64(99)).
		WillReturnRows(sqlmockBoolRow(false))

	_, err := server.CreateCompany(context.Background(), &bankpb.CreateCompanyRequest{
		RegisteredId:   12345,
		Name:           "ACME",
		TaxCode:        999,
		ActivityCodeId: 99,
		Address:        "Main street",
		OwnerId:        1,
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

func TestGetCompanyByIDSuccess(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT id, registered_id, name, tax_code, activity_code_id, address, owner_id").
		WithArgs(int64(10)).
		WillReturnRows(sqlmockCompanyRows().AddRow(int64(10), int64(12345), "ACME", int64(999), int64(2), "Main street", int64(1)))

	resp, err := server.GetCompanyById(context.Background(), &bankpb.GetCompanyByIdRequest{Id: 10})
	if err != nil {
		t.Fatalf("GetCompanyById returned error: %v", err)
	}
	if resp.Company == nil {
		t.Fatalf("expected company in response")
	}
	if resp.Company.Id != 10 || resp.Company.RegisteredId != 12345 || resp.Company.Name != "ACME" {
		t.Fatalf("unexpected company response: %+v", resp.Company)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetCompanyByIDNotFound(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT id, registered_id, name, tax_code, activity_code_id, address, owner_id").
		WithArgs(int64(404)).
		WillReturnError(sql.ErrNoRows)

	_, err := server.GetCompanyById(context.Background(), &bankpb.GetCompanyByIdRequest{Id: 404})
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

func TestGetCompaniesSuccess(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT id, registered_id, name, tax_code, activity_code_id, address, owner_id").
		WillReturnRows(sqlmockCompanyRows().
			AddRow(int64(1), int64(12345), "ACME", int64(999), int64(2), "Main street", int64(1)).
			AddRow(int64(2), int64(54321), "Beta", int64(555), nil, "Side street", int64(2)))

	resp, err := server.GetCompanies(context.Background(), &bankpb.GetCompaniesRequest{})
	if err != nil {
		t.Fatalf("GetCompanies returned error: %v", err)
	}
	if len(resp.Companies) != 2 {
		t.Fatalf("expected 2 companies, got %d", len(resp.Companies))
	}
	if resp.Companies[1].ActivityCodeId != 0 {
		t.Fatalf("expected zero activity code for null DB value, got %d", resp.Companies[1].ActivityCodeId)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestUpdateCompanySuccess(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM companies WHERE id = $1)`)).
		WithArgs(int64(10)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmockBoolRow(true))
	mock.ExpectQuery("UPDATE companies").
		WithArgs("ACME Updated", "Main street 2", int64(1), int64(10)).
		WillReturnRows(sqlmockCompanyRows().AddRow(int64(10), int64(12345), "ACME Updated", int64(999), nil, "Main street 2", int64(1)))
	mock.ExpectCommit()

	resp, err := server.UpdateCompany(context.Background(), &bankpb.UpdateCompanyRequest{
		Id:      10,
		Name:    "ACME Updated",
		Address: "Main street 2",
		OwnerId: 1,
	})
	if err != nil {
		t.Fatalf("UpdateCompany returned error: %v", err)
	}
	if resp.Company == nil || resp.Company.Name != "ACME Updated" {
		t.Fatalf("unexpected company response: %+v", resp.Company)
	}
	if resp.Company.ActivityCodeId != 0 {
		t.Fatalf("expected zero activity code for null DB value")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestUpdateCompanyNotFound(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM companies WHERE id = $1)`)).
		WithArgs(int64(404)).
		WillReturnRows(sqlmockBoolRow(false))

	_, err := server.UpdateCompany(context.Background(), &bankpb.UpdateCompanyRequest{
		Id:      404,
		Name:    "ACME",
		Address: "Main street",
		OwnerId: 1,
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

func sqlmockBoolRow(value bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"exists"}).AddRow(value)
}

func sqlmockCompanyRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "registered_id", "name", "tax_code", "activity_code_id", "address", "owner_id"})
}
