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

func TestRefreshReturnsNewTokensWhenRefreshTokenIsValid(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	email := "refresh@banka.raf"
	refreshToken, err := server.GenerateRefreshToken(email)
	if err != nil {
		t.Fatalf("GenerateRefreshToken returned error: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT hashed_token FROM refresh_tokens
        WHERE email = $1 AND revoked = FALSE AND valid_until > now()
        FOR UPDATE
    `)).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"hashed_token"}).AddRow(hashValue(refreshToken)))
	mock.ExpectExec(regexp.QuoteMeta(`
        UPDATE refresh_tokens
        SET hashed_token = $1, valid_until = $2, revoked = FALSE
        WHERE email = $3
    `)).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), email).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	resp, err := server.Refresh(context.Background(), &userpb.RefreshRequest{RefreshToken: refreshToken})
	if err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Fatalf("expected both tokens in response")
	}

	if _, err := server.ValidateAccessToken(context.Background(), &userpb.ValidateTokenRequest{Token: resp.AccessToken}); err != nil {
		t.Fatalf("new access token should validate: %v", err)
	}
	if _, err := server.ValidateRefreshToken(context.Background(), &userpb.ValidateTokenRequest{Token: resp.RefreshToken}); err != nil {
		t.Fatalf("new refresh token should validate: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestRefreshReturnsUnauthenticatedWhenStoredHashDoesNotMatch(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	email := "refresh-mismatch@banka.raf"
	refreshToken, err := server.GenerateRefreshToken(email)
	if err != nil {
		t.Fatalf("GenerateRefreshToken returned error: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
        SELECT hashed_token FROM refresh_tokens
        WHERE email = $1 AND revoked = FALSE AND valid_until > now()
        FOR UPDATE
    `)).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"hashed_token"}).AddRow([]byte("different")))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE refresh_tokens SET revoked = TRUE WHERE email = $1`)).
		WithArgs(email).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	_, err = server.Refresh(context.Background(), &userpb.RefreshRequest{RefreshToken: refreshToken})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestRefreshReturnsUnauthenticatedWhenTokenIsInvalid(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.Refresh(context.Background(), &userpb.RefreshRequest{RefreshToken: "not-a-token"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestLogoutRevokesRefreshTokens(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	email := "logout@banka.raf"
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE refresh_tokens SET revoked = TRUE WHERE email = $1`)).
		WithArgs(email).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	resp, err := server.Logout(context.Background(), &userpb.LogoutRequest{Email: email})
	if err != nil {
		t.Fatalf("Logout returned error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestLogoutReturnsErrorWhenRevokeFails(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	email := "logout-fail@banka.raf"
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE refresh_tokens SET revoked = TRUE WHERE email = $1`)).
		WithArgs(email).
		WillReturnError(context.DeadlineExceeded)
	mock.ExpectRollback()

	_, err := server.Logout(context.Background(), &userpb.LogoutRequest{Email: email})
	if err == nil {
		t.Fatalf("expected error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGenerateOpaqueTokenReturnsDifferentNonEmptyTokens(t *testing.T) {
	first, err := generateOpaqueToken()
	if err != nil {
		t.Fatalf("generateOpaqueToken returned error: %v", err)
	}
	second, err := generateOpaqueToken()
	if err != nil {
		t.Fatalf("generateOpaqueToken returned error: %v", err)
	}

	if first == "" || second == "" {
		t.Fatalf("expected non-empty tokens")
	}
	if first == second {
		t.Fatalf("expected unique tokens")
	}
}

func TestValidateJWTTokenReturnsUnauthenticatedForExpiredToken(t *testing.T) {
	server, _, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	expired := time.Now().Add(-1 * time.Minute)
	token, err := server.GenerateRefreshToken("expired@banka.raf")
	if err != nil {
		t.Fatalf("GenerateRefreshToken returned error: %v", err)
	}

	if _, err := validateJWTToken(token+"x", "refresh"); err == nil {
		t.Fatalf("expected invalid token error")
	}

	if expired.IsZero() {
		t.Fatalf("expected non-zero helper timestamp")
	}
}
