package user

import (
	"bytes"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	jwt "github.com/golang-jwt/jwt/v5"
)

func TestGetUserByEmailReturnsUserWhenFound(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	email := "found@banka"
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT email, password, salt_password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password, salt_password FROM clients WHERE email = $1
		LIMIT 1
	`)).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"email", "password", "salt_password"}).AddRow(email, []byte{1, 2, 3}, []byte{4, 5, 6}))

	user, err := server.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user == nil {
		t.Fatalf("expected user")
	}
	if !bytes.Equal(user.hashedPassword, []byte{1, 2, 3}) {
		t.Fatalf("unexpected password hash")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetUserByEmailReturnsNilWhenMissing(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	email := "missing@banka"
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT email, password, salt_password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password, salt_password FROM clients WHERE email = $1
		LIMIT 1
	`)).
		WithArgs(email).
		WillReturnError(sql.ErrNoRows)

	user, err := server.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != nil {
		t.Fatalf("expected nil user")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRotateRefreshTokenUpdatesHashWhenMatching(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	const email = "token@banka"
	mock.ExpectBegin()
	tx, err := server.database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	timeStamp := time.Now().Add(time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT hashed_token FROM refresh_tokens
		WHERE email = $1 AND revoked = FALSE AND valid_until > now()
		FOR UPDATE
	`)).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"hashed_token"}).AddRow([]byte{1, 2, 3}))

	newHash := []byte{9, 9, 9}
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE refresh_tokens
		SET hashed_token = $1, valid_until = $2, revoked = FALSE
		WHERE email = $3
	`)).
		WithArgs(newHash, timeStamp, email).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := server.rotateRefreshToken(tx, email, []byte{1, 2, 3}, newHash, timeStamp); err != nil {
		t.Fatalf("rotate error: %v", err)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
		t.Fatalf("rollback: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRotateRefreshTokenRevokesWhenHashMismatch(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	const email = "token@banka"
	mock.ExpectBegin()
	tx, err := server.database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT hashed_token FROM refresh_tokens
		WHERE email = $1 AND revoked = FALSE AND valid_until > now()
		FOR UPDATE
	`)).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"hashed_token"}).AddRow([]byte{5}))

	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE refresh_tokens SET revoked = TRUE WHERE email = $1
	`)).
		WithArgs(email).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()
	if err := server.rotateRefreshToken(tx, email, []byte{6}, []byte{7}, time.Now()); err == nil {
		t.Fatalf("expected error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestInsertRefreshTokenStoresHashedValue(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	builder := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "refresher@banka",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute * 5)),
	})
	token, err := builder.SignedString([]byte("refresh"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO refresh_tokens VALUES ($1, $2, $3, FALSE)
		ON CONFLICT (email) DO UPDATE SET (hashed_token, valid_until, revoked) = (excluded.hashed_token, excluded.valid_until, excluded.revoked)
	`)).
		WithArgs("refresher@banka", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := server.InsertRefreshToken(token); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestInsertRefreshTokenReturnsError(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	builder := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "refresher@banka",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute * 5)),
	})
	token, err := builder.SignedString([]byte("refresh"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO refresh_tokens VALUES ($1, $2, $3, FALSE)
		ON CONFLICT (email) DO UPDATE SET (hashed_token, valid_until, revoked) = (excluded.hashed_token, excluded.valid_until, excluded.revoked)
	`)).
		WithArgs("refresher@banka", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	if err := server.InsertRefreshToken(token); err == nil {
		t.Fatalf("expected error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUpsertPasswordActionTokenStoresRecord(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	tokenHash := []byte{1, 2, 3}
	now := time.Now()
	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO password_action_tokens (email, action_type, hashed_token, valid_until, used)
		VALUES ($1, $2, $3, $4, FALSE)
		ON CONFLICT (email, action_type)
		DO UPDATE SET
			hashed_token = excluded.hashed_token,
			valid_until = excluded.valid_until,
			used = FALSE,
			used_at = NULL
	`)).
		WithArgs("action@banka", "reset", tokenHash, now).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := server.UpsertPasswordActionToken("action@banka", "reset", tokenHash, now); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestConsumePasswordActionTokenReturnsEmailAndType(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	hash := []byte{8, 9}
	mock.ExpectBegin()
	tx, err := server.database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT email, action_type
		FROM password_action_tokens
		WHERE hashed_token = $1 AND used = FALSE AND valid_until > NOW()
		FOR UPDATE
	`)).
		WithArgs(hash).
		WillReturnRows(sqlmock.NewRows([]string{"email", "action_type"}).AddRow("consumed@banka", "reset"))

	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE password_action_tokens
		SET used = TRUE, used_at = NOW()
		WHERE email = $1 AND action_type = $2
	`)).
		WithArgs("consumed@banka", "reset").
		WillReturnResult(sqlmock.NewResult(0, 1))

	email, action, err := server.ConsumePasswordActionToken(tx, hash)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if email != "consumed@banka" || action != "reset" {
		t.Fatalf("unexpected payload")
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
		t.Fatalf("rollback: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestConsumePasswordActionTokenReturnsErrorWhenMissing(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := server.database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT email, action_type
		FROM password_action_tokens
		WHERE hashed_token = $1 AND used = FALSE AND valid_until > NOW()
		FOR UPDATE
	`)).
		WithArgs([]byte{0}).
		WillReturnError(sql.ErrNoRows)

	if _, _, err := server.ConsumePasswordActionToken(tx, []byte{0}); !errors.Is(err, ErrInvalidPasswordActionToken) {
		t.Fatalf("expected invalid token error")
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
		t.Fatalf("rollback: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUpdatePasswordByEmailUpdatesEmployee(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	hashed := []byte{3, 4}
	const email = "employee@banka"
	mock.ExpectBegin()
	tx, err := server.database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE employees
		SET password = $1, updated_at = NOW()
		WHERE email = $2
	`)).
		WithArgs(hashed, email).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := server.UpdatePasswordByEmail(tx, email, hashed); err != nil {
		t.Fatalf("update: %v", err)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
		t.Fatalf("rollback: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUpdatePasswordByEmailReturnsErrorWhenMissing(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	hashed := []byte{3, 4}
	const email = "missing@banka"
	mock.ExpectBegin()
	tx, err := server.database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE employees
		SET password = $1, updated_at = NOW()
		WHERE email = $2
	`)).
		WithArgs(hashed, email).
		WillReturnResult(sqlmock.NewResult(0, 0))

	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE clients
		SET password = $1, updated_at = NOW()
		WHERE email = $2
	`)).
		WithArgs(hashed, email).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := server.UpdatePasswordByEmail(tx, email, hashed); err == nil {
		t.Fatalf("expected error")
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
		t.Fatalf("rollback: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRevokeRefreshTokensByEmailMarksAllRevoked(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := server.database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE refresh_tokens SET revoked = TRUE WHERE email = $1`)).
		WithArgs("revoked@banka").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := server.RevokeRefreshTokensByEmail(tx, "revoked@banka"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
		t.Fatalf("rollback: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetClientByIDReturnsClient(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id, first_name, last_name, date_of_birth, gender, email, phone_number, address
		FROM clients
		WHERE id = $1
	`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "first_name", "last_name", "date_of_birth", "gender", "email", "phone_number", "address"}).
			AddRow(7, "Miloš", "Milošević", time.Now(), "M", "milos@banka", "062", "Bulevar"))

	client, err := server.GetClientByID(7)
	if err != nil {
		t.Fatalf("get client: %v", err)
	}
	if client.Id != 7 {
		t.Fatalf("unexpected id")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetClientByIDReturnsErrorWhenMissing(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id, first_name, last_name, date_of_birth, gender, email, phone_number, address
		FROM clients
		WHERE id = $1
	`)).
		WithArgs(int64(9)).
		WillReturnError(sql.ErrNoRows)

	if _, err := server.GetClientByID(9); err == nil {
		t.Fatalf("expected error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetAllClientsAppliesFilters(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, first_name, last_name, date_of_birth, gender, email, phone_number, address FROM clients WHERE first_name = $1 AND last_name = $2 AND email = $3 ORDER BY last_name ASC, first_name ASC`)).
		WithArgs("Ana", "Perić", "ana@banka").
		WillReturnRows(sqlmock.NewRows([]string{"id", "first_name", "last_name", "date_of_birth", "gender", "email", "phone_number", "address"}).
			AddRow(1, "Ana", "Perić", time.Now(), "F", "ana@banka", "060", "addr"))

	clients, err := server.GetAllClients("Ana", "Perić", "ana@banka")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("expected 1 client")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
