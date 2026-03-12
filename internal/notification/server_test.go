package notification

import (
	"context"
	"testing"

	notificationpb "banka-raf/gen/notification"
)

func setSMTPTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("FROM_EMAIL", "test@example.com")
	t.Setenv("FROM_EMAIL_PASSWORD", "test-password")
	t.Setenv("FROM_EMAIL_SMTP", "smtp.example.com")
	t.Setenv("SMTP_ADDR", "127.0.0.1:1")
}

func TestSendPasswordResetEmailSMTPFailureReturnsUnsuccessful(t *testing.T) {
	setSMTPTestEnv(t)

	server := &Server{}
	resp, err := server.SendPasswordResetEmail(context.Background(), &notificationpb.PasswordLinkMailRequest{
		ToAddr: "receiver@example.com",
		Link:   "https://frontend/reset-password?token=abc",
	})
	if err != nil {
		t.Fatalf("SendPasswordResetEmail returned unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected response, got nil")
	}
	if resp.Successful {
		t.Fatalf("expected unsuccessful=false due to smtp failure")
	}
}

func TestSendInitialPasswordSetEmailSMTPFailureReturnsUnsuccessful(t *testing.T) {
	setSMTPTestEnv(t)

	server := &Server{}
	resp, err := server.SendInitialPasswordSetEmail(context.Background(), &notificationpb.PasswordLinkMailRequest{
		ToAddr: "receiver@example.com",
		Link:   "https://frontend/set-password?token=abc",
	})
	if err != nil {
		t.Fatalf("SendInitialPasswordSetEmail returned unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected response, got nil")
	}
	if resp.Successful {
		t.Fatalf("expected unsuccessful=false due to smtp failure")
	}
}
