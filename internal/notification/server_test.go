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

type successSender struct{}

func (s *successSender) Send(to []string, subject string, body string) error {
	return nil
}

func TestSendConfirmationEmailSuccess(t *testing.T) {
	server := NewServer(&successSender{})
	resp, err := server.SendConfirmationEmail(context.Background(), &notificationpb.ConfirmationMailRequest{
		ToAddr:  "receiver@example.com",
		Subject: "Test",
		Body:    "123",
	})
	if err != nil {
		t.Fatalf("SendConfirmationEmail error: %v", err)
	}
	if !resp.Successful {
		t.Fatalf("expected successful response")
	}
}

func TestSendActivationEmailSuccess(t *testing.T) {
	server := NewServer(&successSender{})
	resp, err := server.SendActivationEmail(context.Background(), &notificationpb.ActivationMailRequest{
		ToAddr: "receiver@example.com",
		Link:   "http://test",
	})
	if err != nil {
		t.Fatalf("SendActivationEmail error: %v", err)
	}
	if !resp.Successful {
		t.Fatalf("expected successful response")
	}
}

func TestSendPasswordResetEmailSuccess(t *testing.T) {
	server := NewServer(&successSender{})
	resp, err := server.SendPasswordResetEmail(context.Background(), &notificationpb.PasswordLinkMailRequest{
		ToAddr: "receiver@example.com",
		Link:   "http://test",
	})
	if err != nil {
		t.Fatalf("SendPasswordResetEmail error: %v", err)
	}
	if !resp.Successful {
		t.Fatalf("expected successful response")
	}
}

func TestSendInitialPasswordSetEmailSuccess(t *testing.T) {
	server := NewServer(&successSender{})
	resp, err := server.SendInitialPasswordSetEmail(context.Background(), &notificationpb.PasswordLinkMailRequest{
		ToAddr: "receiver@example.com",
		Link:   "http://test",
	})
	if err != nil {
		t.Fatalf("SendInitialPasswordSetEmail error: %v", err)
	}
	if !resp.Successful {
		t.Fatalf("expected successful response")
	}
}

func TestSendCardConfirmationEmailSuccess(t *testing.T) {
	server := NewServer(&successSender{})
	resp, err := server.SendCardConfirmationEmail(context.Background(), &notificationpb.CardConfirmationMailRequest{
		ToAddr: "receiver@example.com",
		Link:   "http://test",
	})
	if err != nil {
		t.Fatalf("SendCardConfirmationEmail error: %v", err)
	}
	if !resp.Successful {
		t.Fatalf("expected successful response")
	}
}

func TestSendCardCreatedEmailSuccess(t *testing.T) {
	server := NewServer(&successSender{})
	resp, err := server.SendCardCreatedEmail(context.Background(), &notificationpb.CardCreatedMailRequest{
		ToAddr: "receiver@example.com",
	})
	if err != nil {
		t.Fatalf("SendCardCreatedEmail error: %v", err)
	}
	if !resp.Successful {
		t.Fatalf("expected successful response")
	}
}

func TestSMTPSender(t *testing.T) {
	setSMTPTestEnv(t)
	// Just test it doesn't panic natively. We expect it to try and dial 127.0.0.1:1 and fail.
	sender := &SMTPSender{}
	err := sender.Send([]string{"test@test.com"}, "test", "test")
	if err == nil {
		t.Fatalf("expected error from bogus SMTP server")
	}
}

func TestTemplateParseErrors(t *testing.T) {
	server := NewServer(&successSender{})
	// Switch to a directory without templates to force ParseFiles error
	t.Chdir(t.TempDir()) // sets working dir to empty temp directory

	ctx := context.Background()

	resp1, _ := server.SendConfirmationEmail(ctx, &notificationpb.ConfirmationMailRequest{})
	if resp1.Successful {
		t.Fatalf("expected false")
	}

	resp2, _ := server.SendActivationEmail(ctx, &notificationpb.ActivationMailRequest{})
	if resp2.Successful {
		t.Fatalf("expected false")
	}

	resp3, _ := server.SendPasswordResetEmail(ctx, &notificationpb.PasswordLinkMailRequest{})
	if resp3.Successful {
		t.Fatalf("expected false")
	}

	resp4, _ := server.SendInitialPasswordSetEmail(ctx, &notificationpb.PasswordLinkMailRequest{})
	if resp4.Successful {
		t.Fatalf("expected false")
	}

	resp5, _ := server.SendCardConfirmationEmail(ctx, &notificationpb.CardConfirmationMailRequest{})
	if resp5.Successful {
		t.Fatalf("expected false")
	}

	resp6, _ := server.SendCardCreatedEmail(ctx, &notificationpb.CardCreatedMailRequest{})
	if resp6.Successful {
		t.Fatalf("expected false")
	}
}
