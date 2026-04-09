package user

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLoginNonExistantUser(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	email := "missing@banka.raf"
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT email, password, salt_password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password, salt_password FROM clients WHERE email = $1
		LIMIT 1
	`)).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"email", "password", "salt_password"}))
	resp, err := server.Login(context.Background(), &userpb.LoginRequest{Email: email, Password: "password"})
	if err != nil {
		if status.Code(err) == codes.Unauthenticated {
			return
		}
		t.Fatalf("got error other than Unauthenticated")
	}
	if len(resp.AccessToken) > 0 || len(resp.RefreshToken) > 0 {
		t.Fatalf("expected login to fail but got tokens")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	email := "admin@banka.raf"
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT email, password, salt_password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password, salt_password FROM clients WHERE email = $1
		LIMIT 1
	`)).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"email", "password", "salt_password"}))
	resp, err := server.Login(context.Background(), &userpb.LoginRequest{Email: email, Password: "wrong password"})
	if err != nil {
		if status.Code(err) == codes.Unauthenticated {
			return
		}
		t.Fatalf("got error other than Unauthenticated")
	}
	if len(resp.AccessToken) > 0 || len(resp.RefreshToken) > 0 {
		t.Fatalf("expected login to fail but got tokens")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestLoginCorrectCreds(t *testing.T) {
	server, mock, db := newFullTestServer(t)
	defer func() { _ = db.Close() }()

	mockPassword := HashPassword("password", []byte{3, 2, 1})

	email := "admin@banka.raf"
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT email, password, salt_password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password, salt_password FROM clients WHERE email = $1
		LIMIT 1
	`)).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"email", "password", "salt_password"}).AddRow(email, mockPassword, []byte{3, 2, 1}))

	// getRoleAndPermissions queries for employee — return no rows (user is a client)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "employees" WHERE email = $1 ORDER BY "employees"."id" LIMIT $2`)).
		WithArgs(email, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "first_name", "last_name", "date_of_birth", "gender", "email", "phone_number", "address", "username", "password", "salt_password", "position", "department", "active", "created_at", "updated_at"}))

	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO refresh_tokens VALUES ($1, $2, $3, FALSE)
		ON CONFLICT (email) DO UPDATE SET (hashed_token, valid_until, revoked) = (excluded.hashed_token, excluded.valid_until, excluded.revoked)
	`)).WithArgs(email, sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	resp, err := server.Login(context.Background(), &userpb.LoginRequest{Email: email, Password: "password"})
	if err != nil {
		t.Fatalf("got error in Login: %v", err)
	}

	accessToken := resp.AccessToken
	refreshToken := resp.RefreshToken
	if len(accessToken) == 0 || len(refreshToken) == 0 {
		t.Fatalf("expected to get tokens")
	}

	_, err = server.ValidateAccessToken(context.Background(), &userpb.ValidateTokenRequest{Token: accessToken})
	if err != nil {
		t.Fatalf("couldn't validate access token: %v", err)
	}

	_, err = server.ValidateRefreshToken(context.Background(), &userpb.ValidateTokenRequest{Token: refreshToken})
	if err != nil {
		t.Fatalf("couldn't validate refresh token")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
