package user

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
)

var userIdQuery = regexp.QuoteMeta(`SELECT id FROM employees WHERE email = $1 UNION ALL SELECT id FROM clients WHERE email = $1 LIMIT 1`)

func TestTOTP_EnrollBegin(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to open mock sql db, got error: %v", err)
	}
	defer db.Close()

	srv := NewTotpServer(db)
	ctx := context.Background()

	email := "totp@banka.rs"
	clientId := uint64(1)

	// mock getUserIdByEmail
	mock.ExpectQuery(userIdQuery).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(clientId))

	// mock SetTempTOTPSecret
	mock.ExpectExec(regexp.QuoteMeta(`
                INSERT INTO verification_codes (client_id, temp_secret, temp_created_at)
                VALUES ($1, $2, NOW())
                ON CONFLICT (client_id)
                DO UPDATE SET
                        temp_secret = EXCLUDED.temp_secret,
                        temp_created_at = NOW()
        `)).
		WithArgs(clientId, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := &userpb.EnrollBeginRequest{Email: email}
	resp, err := srv.EnrollBegin(ctx, req)
	assert.NoError(t, err)
	if resp != nil {
		assert.NotEmpty(t, resp.Url)
		assert.Contains(t, resp.Url, "totp@banka.rs")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestTOTP_EnrollConfirmAndVerify(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("Failed to open mock sql db, got error: %v", err)
	}
	defer db.Close()

	srv := NewTotpServer(db)
	ctx := context.Background()

	email := "totp@banka.rs"
	clientId := uint64(1)

	// Create real secret
	key, _ := totp.Generate(totp.GenerateOpts{
		Issuer:      "Banka3",
		AccountName: email,
	})
	secret := key.Secret()
	code, _ := totp.GenerateCodeCustom(secret, time.Now(), totp.ValidateOpts{
		Digits: 6,
		Period: 30,
		Skew:   1,
	})

	// ---------- EnrollConfirm ----------
	mock.ExpectQuery(userIdQuery).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(clientId))

	mock.ExpectBegin()

	mock.ExpectQuery(regexp.QuoteMeta(`
                SELECT temp_secret
                FROM verification_codes
                WHERE client_id = $1
                FOR UPDATE
        `)).
		WithArgs(clientId).
		WillReturnRows(sqlmock.NewRows([]string{"temp_secret"}).AddRow(secret))

	mock.ExpectExec(regexp.QuoteMeta(`
                UPDATE verification_codes
                SET enabled = TRUE,
                    secret = $1,
                    temp_secret = NULL
                WHERE client_id = $2
        `)).
		WithArgs(secret, clientId).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	confirmReq := &userpb.EnrollConfirmRequest{Email: email, Code: code}
	confirmResp, err := srv.EnrollConfirm(ctx, confirmReq)
	assert.NoError(t, err)
	if confirmResp != nil {
		assert.True(t, confirmResp.Success)
	}

	// ---------- VerifyCode ----------
	mock.ExpectQuery(userIdQuery).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(clientId))

	mock.ExpectQuery(regexp.QuoteMeta(`
                SELECT secret
                FROM verification_codes
                WHERE client_id = $1 AND enabled = TRUE
        `)).
		WithArgs(clientId).
		WillReturnRows(sqlmock.NewRows([]string{"secret"}).AddRow(secret))

	verifyReq := &userpb.VerifyCodeRequest{Email: email, Code: code}
	verifyResp, err := srv.VerifyCode(ctx, verifyReq)
	assert.NoError(t, err)
	if verifyResp != nil {
		assert.True(t, verifyResp.Valid)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestTOTP_EnrollConfirm_UserNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	srv := NewTotpServer(db)
	ctx := context.Background()

	email := "dummy@mail.com"

	mock.ExpectQuery(userIdQuery).
		WithArgs(email).
		WillReturnError(sql.ErrNoRows)

	req := &userpb.EnrollConfirmRequest{Email: email, Code: "123456"}
	_, err = srv.EnrollConfirm(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "user not found")
}

func TestTOTP_VerifyCode_UserNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	srv := NewTotpServer(db)
	ctx := context.Background()

	email := "dummy@mail.com"

	mock.ExpectQuery(userIdQuery).
		WithArgs(email).
		WillReturnError(sql.ErrNoRows)

	req := &userpb.VerifyCodeRequest{Email: email, Code: "123456"}
	_, err = srv.VerifyCode(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "user not found")
}

func TestTOTP_VerifyCode_SecretNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	srv := NewTotpServer(db)
	ctx := context.Background()
	email := "has@banka.rs"
	clientId := uint64(1)

	mock.ExpectQuery(userIdQuery).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(clientId))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT secret FROM verification_codes WHERE client_id = $1 AND enabled = TRUE`)).
		WithArgs(clientId).
		WillReturnError(sql.ErrNoRows)

	req := &userpb.VerifyCodeRequest{Email: email, Code: "123456"}
	_, err = srv.VerifyCode(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "user doesn't have TOTP set up")
}

func TestTOTP_EnrollConfirm_TempSecretNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	srv := NewTotpServer(db)
	ctx := context.Background()
	email := "has@banka.rs"
	clientId := uint64(1)

	mock.ExpectQuery(userIdQuery).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(clientId))

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT temp_secret FROM verification_codes WHERE client_id = $1 FOR UPDATE`)).
		WithArgs(clientId).
		WillReturnError(sql.ErrNoRows) // Maps to ErrUserNotFound by repo
	mock.ExpectRollback()

	req := &userpb.EnrollConfirmRequest{Email: email, Code: "123456"}
	_, err = srv.EnrollConfirm(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), sql.ErrNoRows.Error())
}

func TestTOTP_EnrollConfirm_InvalidCode(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	srv := NewTotpServer(db)
	ctx := context.Background()

	email := "totp@banka.rs"
	clientId := uint64(1)

	// Create real secret
	key, _ := totp.Generate(totp.GenerateOpts{
		Issuer:      "Banka3",
		AccountName: email,
	})
	secret := key.Secret()

	mock.ExpectQuery(userIdQuery).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(clientId))

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT temp_secret FROM verification_codes WHERE client_id = $1 FOR UPDATE`)).
		WithArgs(clientId).
		WillReturnRows(sqlmock.NewRows([]string{"temp_secret"}).AddRow(secret))
	mock.ExpectRollback() // Rollback called due to defer when returning early

	req := &userpb.EnrollConfirmRequest{Email: email, Code: "000000"} // deliberately wrong code
	resp, err := srv.EnrollConfirm(ctx, req)
	assert.NoError(t, err)
	assert.False(t, resp.Success)
}

func TestTOTP_EnrollBegin_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	srv := NewTotpServer(db)
	ctx := context.Background()

	email := "totp@banka.rs"
	clientId := uint64(1)

	mock.ExpectQuery(userIdQuery).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(clientId))

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO verification_codes (client_id, temp_secret, temp_created_at)`)).
		WithArgs(clientId, sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	req := &userpb.EnrollBeginRequest{Email: email}
	_, err = srv.EnrollBegin(ctx, req)
	assert.Error(t, err)
}
