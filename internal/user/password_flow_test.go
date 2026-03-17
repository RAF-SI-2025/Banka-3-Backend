package user

import (
	"context"
	"database/sql"
	"net/url"
	"regexp"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
)

type testNotificationServer struct {
	notificationpb.UnimplementedNotificationServiceServer
	mu            sync.Mutex
	resetRequests []*notificationpb.PasswordLinkMailRequest
	setRequests   []*notificationpb.PasswordLinkMailRequest
}

func (s *testNotificationServer) SendPasswordResetEmail(_ context.Context, req *notificationpb.PasswordLinkMailRequest) (*notificationpb.SuccessResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetRequests = append(s.resetRequests, req)
	return &notificationpb.SuccessResponse{Successful: true}, nil
}

func (s *testNotificationServer) SendInitialPasswordSetEmail(_ context.Context, req *notificationpb.PasswordLinkMailRequest) (*notificationpb.SuccessResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setRequests = append(s.setRequests, req)
	return &notificationpb.SuccessResponse{Successful: true}, nil
}

func TestRequestPasswordResetUnknownEmailReturnsAccepted(t *testing.T) {
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

	resp, err := server.RequestPasswordReset(context.Background(), &userpb.PasswordActionRequest{Email: email})
	if err != nil {
		t.Fatalf("RequestPasswordReset returned error: %v", err)
	}
	if !resp.Accepted {
		t.Fatalf("expected accepted=true, got false")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestRequestPasswordResetExistingEmailSendsNotification(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	notificationServer := &testNotificationServer{}
	addr, stop := startNotificationTestServer(t, notificationServer)
	defer stop()

	t.Setenv("NOTIFICATION_GRPC_ADDR", addr)
	t.Setenv("PASSWORD_RESET_BASE_URL", "https://frontend/reset-password")

	email := "admin@banka.raf"
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT email, password, salt_password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password, salt_password FROM clients WHERE email = $1
		LIMIT 1
	`)).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"email", "password", "salt_password"}).AddRow(email, []byte{1, 2, 3}, []byte{3, 2, 1}))
	mock.ExpectExec("INSERT INTO password_action_tokens").
		WithArgs(email, passwordActionReset, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	resp, err := server.RequestPasswordReset(context.Background(), &userpb.PasswordActionRequest{Email: email})
	if err != nil {
		t.Fatalf("RequestPasswordReset returned error: %v", err)
	}
	if !resp.Accepted {
		t.Fatalf("expected accepted=true, got false")
	}

	notificationServer.mu.Lock()
	defer notificationServer.mu.Unlock()
	if len(notificationServer.resetRequests) != 1 {
		t.Fatalf("expected 1 reset email request, got %d", len(notificationServer.resetRequests))
	}
	if len(notificationServer.setRequests) != 0 {
		t.Fatalf("expected 0 initial set email requests, got %d", len(notificationServer.setRequests))
	}

	req := notificationServer.resetRequests[0]
	if req.ToAddr != email {
		t.Fatalf("expected email %s, got %s", email, req.ToAddr)
	}

	link, err := url.Parse(req.Link)
	if err != nil {
		t.Fatalf("invalid link: %v", err)
	}
	if link.Scheme != "https" || link.Host != "frontend" || link.Path != "/reset-password" {
		t.Fatalf("unexpected reset link: %s", req.Link)
	}
	if link.Query().Get("token") == "" {
		t.Fatalf("expected token query parameter in link")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestRequestInitialPasswordSetExistingEmailSendsNotification(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	notificationServer := &testNotificationServer{}
	addr, stop := startNotificationTestServer(t, notificationServer)
	defer stop()

	t.Setenv("NOTIFICATION_GRPC_ADDR", addr)
	t.Setenv("PASSWORD_SET_BASE_URL", "https://frontend/set-password")

	email := "client@banka.raf"
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT email, password, salt_password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password, salt_password FROM clients WHERE email = $1
		LIMIT 1
	`)).
		WithArgs(email).
		WillReturnRows(sqlmock.NewRows([]string{"email", "password", "salt_password"}).AddRow(email, []byte{9, 9, 9}, []byte{7, 7, 7}))
	mock.ExpectExec("INSERT INTO password_action_tokens").
		WithArgs(email, passwordActionInitialSet, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	resp, err := server.RequestInitialPasswordSet(context.Background(), &userpb.PasswordActionRequest{Email: email})
	if err != nil {
		t.Fatalf("RequestInitialPasswordSet returned error: %v", err)
	}
	if !resp.Accepted {
		t.Fatalf("expected accepted=true, got false")
	}

	notificationServer.mu.Lock()
	defer notificationServer.mu.Unlock()
	if len(notificationServer.setRequests) != 1 {
		t.Fatalf("expected 1 initial set email request, got %d", len(notificationServer.setRequests))
	}
	if len(notificationServer.resetRequests) != 0 {
		t.Fatalf("expected 0 reset email requests, got %d", len(notificationServer.resetRequests))
	}

	req := notificationServer.setRequests[0]
	link, err := url.Parse(req.Link)
	if err != nil {
		t.Fatalf("invalid link: %v", err)
	}
	if link.Scheme != "https" || link.Host != "frontend" || link.Path != "/set-password" {
		t.Fatalf("unexpected set-password link: %s", req.Link)
	}
	if link.Query().Get("token") == "" {
		t.Fatalf("expected token query parameter in link")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestSetPasswordWithTokenInvalidInput(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.SetPasswordWithToken(context.Background(), &userpb.SetPasswordWithTokenRequest{
		Token:       "",
		NewPassword: "new-pass",
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

func TestSetPasswordWithTokenSuccess(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	email := "admin@banka.raf"
	token := "opaque-token"
	newPassword := "Admin123!"

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT email, action_type").
		WithArgs(hashValue(token)).
		WillReturnRows(sqlmock.NewRows([]string{"email", "action_type"}).AddRow(email, passwordActionReset))
	mock.ExpectExec("UPDATE password_action_tokens").
		WithArgs(email, passwordActionReset).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE employees").
		WithArgs(hashValue(newPassword), email).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE refresh_tokens SET revoked = TRUE WHERE email = \\$1").
		WithArgs(email).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	resp, err := server.SetPasswordWithToken(context.Background(), &userpb.SetPasswordWithTokenRequest{
		Token:       token,
		NewPassword: newPassword,
	})
	if err != nil {
		t.Fatalf("SetPasswordWithToken returned error: %v", err)
	}
	if !resp.Successful {
		t.Fatalf("expected successful=true, got false")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestSetPasswordWithTokenInvalidOrExpiredToken(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT email, action_type").
		WithArgs(hashValue("expired-token")).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, err := server.SetPasswordWithToken(context.Background(), &userpb.SetPasswordWithTokenRequest{
		Token:       "expired-token",
		NewPassword: "new-pass",
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
