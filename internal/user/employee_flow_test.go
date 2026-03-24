package user

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func sqlmockEmployeeRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "first_name", "last_name", "date_of_birth", "gender", "email", "phone_number",
		"address", "username", "password", "salt_password", "position", "department", "active", "created_at", "updated_at",
	})
}

func TestCreateClientAccountRejectsMissingRequiredField(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.CreateClientAccount(context.Background(), &userpb.CreateClientRequest{
		FirstName: "A",
		LastName:  "B",
		Gender:    "M",
		Email:     "",
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

func TestCreateClientAccountCreatesClientWhenInputIsValid(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "clients"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	resp, err := server.CreateClientAccount(context.Background(), &userpb.CreateClientRequest{
		FirstName:   "Ana",
		LastName:    "Anic",
		BirthDate:   time.Now().Unix(),
		Gender:      "F",
		Email:       "ana@banka.raf",
		PhoneNumber: "+38161123456",
		Address:     "Test 1",
		Password:    "Secret1!",
	})
	if err != nil {
		t.Fatalf("CreateClientAccount returned error: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected valid=true")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreateEmployeeAccountRejectsMissingRequiredField(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.CreateEmployeeAccount(context.Background(), &userpb.CreateEmployeeRequest{
		FirstName: "Ana",
		LastName:  "Anic",
		Email:     "",
		Username:  "aanic",
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

func TestCreateEmployeeAccountCreatesEmployeeEvenWhenActivationEmailFlowFails(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	t.Setenv("PASSWORD_SET_BASE_URL", "")

	email := "employee@banka.raf"
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "employees"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT email, password, salt_password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password, salt_password FROM clients WHERE email = $1
		LIMIT 1
	`)).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"email", "password", "salt_password"}).AddRow(email, []byte{1}, []byte{2}))
	mock.ExpectExec("INSERT INTO password_action_tokens").
		WithArgs(email, passwordActionInitialSet, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	resp, err := server.CreateEmployeeAccount(context.Background(), &userpb.CreateEmployeeRequest{
		FirstName:   "Emp",
		LastName:    "Loyee",
		BirthDate:   time.Now().Unix(),
		Gender:      "M",
		Email:       email,
		PhoneNumber: "+38161111222",
		Address:     "Address",
		Username:    "employee",
		Position:    "advisor",
		Department:  "retail",
	})
	if err != nil {
		t.Fatalf("CreateEmployeeAccount returned error: %v", err)
	}
	if resp.Email != email {
		t.Fatalf("expected email %s, got %s", email, resp.Email)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetEmployeeByEmailReturnsErrorWhenRepositoryFails(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT .* FROM "employees"`).WillReturnError(assertiveErr("db down"))

	_, err := server.GetEmployeeByEmail(context.Background(), &userpb.GetEmployeeByEmailRequest{Email: "nobody@banka.raf"})
	if err == nil {
		t.Fatalf("expected error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestDeleteEmployeeReturnsNotFoundWhenNothingIsDeleted(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "employees" WHERE "employees"."id" = \$1`).WithArgs(int64(99)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	_, err := server.DeleteEmployee(context.Background(), &userpb.DeleteEmployeeRequest{Id: 99})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestUpdateEmployeeReturnsNotFoundWhenUpdateFails(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "employees" SET`).WillReturnError(assertiveErr("update failed"))
	mock.ExpectRollback()

	_, err := server.UpdateEmployee(context.Background(), &userpb.UpdateEmployeeRequest{
		Id:          5,
		FirstName:   "A",
		LastName:    "B",
		Gender:      "M",
		PhoneNumber: "1",
		Address:     "2",
		Position:    "3",
		Department:  "4",
		Active:      true,
		Permissions: []string{"read"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

type assertiveErr string

func (e assertiveErr) Error() string { return string(e) }

func TestGetEmployeeByIdReturnsErrorWhenRepositoryFails(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT .* FROM "employees"`).WillReturnError(assertiveErr("db down"))

	_, err := server.GetEmployeeById(context.Background(), &userpb.GetEmployeeByIdRequest{Id: 2})
	if err == nil {
		t.Fatalf("expected error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetAllEmployeesReturnsRowsWithFilters(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	email := "advisor@banka.raf"
	first := "Adv"
	last := "Sor"
	position := "advisor"

	mock.ExpectQuery(`SELECT .* FROM "employees"`).
		WillReturnRows(sqlmockEmployeeRows().
			AddRow(uint64(10), "Adv", "Sor", time.Now(), "M", email, "+38161111111", "Addr", "advisor", []byte{1}, []byte{2}, position, "retail", true, time.Now(), time.Now()))
	mock.ExpectQuery(`SELECT .* FROM "employee_permissions"`).WillReturnRows(sqlmock.NewRows([]string{"employee_id", "permission_id"}))

	employees, err := server.GetAllEmployees(&email, &first, &last, &position)
	if err != nil {
		t.Fatalf("GetAllEmployees returned error: %v", err)
	}
	if len(employees) != 1 {
		t.Fatalf("expected one employee")
	}
	if employees[0].Email != email {
		t.Fatalf("unexpected employee email")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetEmployeesMapsRepositoryModelsToProto(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	email := "map@banka.raf"
	mock.ExpectQuery(`SELECT .* FROM "employees"`).
		WillReturnRows(sqlmockEmployeeRows().
			AddRow(uint64(11), "Map", "Ping", time.Now(), "F", email, "+38162222222", "Addr", "mapping", []byte{1}, []byte{2}, "officer", "ops", true, time.Now(), time.Now()))
	mock.ExpectQuery(`SELECT .* FROM "employee_permissions"`).WillReturnRows(sqlmock.NewRows([]string{"employee_id", "permission_id"}))

	resp, err := server.GetEmployees(context.Background(), &userpb.GetEmployeesRequest{Email: email})
	if err != nil {
		t.Fatalf("GetEmployees returned error: %v", err)
	}
	if len(resp.Employees) != 1 {
		t.Fatalf("expected one employee in response")
	}
	if resp.Employees[0].Email != email {
		t.Fatalf("unexpected employee email")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestUpdateEmployeeReturnsUnknownPermissionWhenPermissionQueryFails(t *testing.T) {
	server, mock, db := newGormTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "employees" SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT .* FROM "permissions"`).WillReturnError(assertiveErr("permission query failed"))
	mock.ExpectRollback()

	_, err := server.UpdateEmployee(context.Background(), &userpb.UpdateEmployeeRequest{
		Id:          6,
		FirstName:   "N",
		LastName:    "P",
		Gender:      "M",
		PhoneNumber: "1",
		Address:     "2",
		Position:    "3",
		Department:  "4",
		Active:      true,
		Permissions: []string{"missing"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
