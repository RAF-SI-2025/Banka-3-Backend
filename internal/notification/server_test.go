package notification

import (
	"context"
	"errors"
	"testing"

	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
)

type failingSender struct{}

func (f *failingSender) Send(_ []string, _ string, _ string) error {
	return errors.New("send failed")
}

func setSMTPTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("FROM_EMAIL", "test@example.com")
	t.Setenv("FROM_EMAIL_PASSWORD", "test-password")
	t.Setenv("FROM_EMAIL_SMTP", "smtp.example.com")
	t.Setenv("SMTP_ADDR", "127.0.0.1:1")
}

func TestSendPasswordResetEmailSMTPFailureReturnsUnsuccessful(t *testing.T) {
	setSMTPTestEnv(t)

	server := &Server{sender: &failingSender{}}
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

	server := &Server{sender: &failingSender{}}
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

func TestSendCardConfirmationEmailSMTPFailureReturnsUnsuccessful(t *testing.T) {
	setSMTPTestEnv(t)

	server := &Server{sender: &failingSender{}}
	resp, err := server.SendCardConfirmationEmail(context.Background(), &notificationpb.CardConfirmationMailRequest{
		ToAddr: "receiver@example.com",
		Link:   "https://frontend/confirm-card?token=abc",
	})
	if err != nil {
		t.Fatalf("SendCardConfirmationEmail returned unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected response, got nil")
	}
	if resp.Successful {
		t.Fatalf("expected unsuccessful=false due to smtp failure")
	}
}

func TestSendCardCreatedEmailSMTPFailureReturnsUnsuccessful(t *testing.T) {
	setSMTPTestEnv(t)

	server := &Server{sender: &failingSender{}}
	resp, err := server.SendCardCreatedEmail(context.Background(), &notificationpb.CardCreatedMailRequest{
		ToAddr: "receiver@example.com",
	})
	if err != nil {
		t.Fatalf("SendCardCreatedEmail returned unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected response, got nil")
	}
	if resp.Successful {
		t.Fatalf("expected unsuccessful=false due to smtp failure")
	}
}

func TestSendLoanRequestCreatedEmailSMTPFailureReturnsUnsuccessful(t *testing.T) {
	setSMTPTestEnv(t)

	server := &Server{sender: &failingSender{}}
	resp, err := server.SendLoanRequestCreatedEmail(context.Background(), &notificationpb.LoanRequestCreatedMailRequest{
		ToAddr: "receiver@example.com",
	})
	if err != nil {
		t.Fatalf("SendLoanRequestCreatedEmail returned unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected response, got nil")
	}
	if resp.Successful {
		t.Fatalf("expected unsuccessful=false due to smtp failure")
	}
}
