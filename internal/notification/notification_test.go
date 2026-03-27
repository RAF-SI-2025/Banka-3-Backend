package notification

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
)

type MockSender struct {
	ShouldFail bool
}

func (m *MockSender) Send(_ []string, _ string, _ string) error {
	if m.ShouldFail {
		return errors.New("failed to send email")
	}
	return nil
}

// funkcija za kreiranje fake templejta

func createFakeTemplate(path string, t *testing.T) {
	t.Helper()
	err := os.MkdirAll("test-templates", 0755)
	if err != nil {
		t.Fatalf("failed to create templates dir: %v", err)
	}
	content := []byte("<h1>Test Template</h1>")
	err = os.WriteFile(path, content, 0644)
	if err != nil {
		t.Fatalf("failed to write template: %v", err)
	}
}

// Cleanup templejte nakon testova
func cleanupTemplates(t *testing.T) {
	t.Helper()
	err := os.RemoveAll("test-templates")
	if err != nil {
		t.Fatalf("failed to cleanup templates: %v", err)
	}
}

// TESTOVI ZA SENDCONIRMATIONEMAIL
func TestSendConfirmationEmail_Success(t *testing.T) {
	createFakeTemplate("test-templates/confirmation.html", t)
	defer cleanupTemplates(t)

	mock := &MockSender{ShouldFail: false}
	server := &Server{sender: mock}

	req := &notification.ConfirmationMailRequest{ToAddr: "test@test.com"}
	resp, err := server.SendConfirmationEmail(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Successful {
		t.Fatalf("expected Successful=true, got false")
	}
}

func TestSendConfirmationEmail_Fail(t *testing.T) {
	createFakeTemplate("test-templates/confirmation.html", t)
	defer cleanupTemplates(t)

	mock := &MockSender{ShouldFail: true}
	server := &Server{sender: mock}

	req := &notification.ConfirmationMailRequest{ToAddr: "test@test.com"}
	resp, _ := server.SendConfirmationEmail(context.Background(), req)
	if resp.Successful {
		t.Fatalf("expected Successful=false, got true")
	}
}

// TESTOVI ZA SENDACTIVATIONEMAIL
func TestSendActivationEmail_Success(t *testing.T) {
	createFakeTemplate("test-templates/activation.html", t)
	defer cleanupTemplates(t)

	mock := &MockSender{ShouldFail: false}
	server := &Server{sender: mock}

	req := &notification.ActivationMailRequest{ToAddr: "test@test.com"}
	resp, err := server.SendActivationEmail(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Successful {
		t.Errorf("expected Successful=true, got false")
	}
}

func TestSendActivationEmail_Fail(t *testing.T) {
	createFakeTemplate("test-templates/activation.html", t)
	defer cleanupTemplates(t)

	mock := &MockSender{ShouldFail: true}
	server := &Server{sender: mock}

	req := &notification.ActivationMailRequest{ToAddr: "test@test.com"}
	resp, _ := server.SendActivationEmail(context.Background(), req)
	if resp.Successful {
		t.Errorf("expected Successful=false, got true")
	}
}
func TestSendCardConfirmationEmail_Success(t *testing.T) {
	createFakeTemplate("test-templates/card_confirmation.html", t)
	defer cleanupTemplates(t)

	mock := &MockSender{ShouldFail: false}
	server := &Server{sender: mock}

	req := &notification.CardConfirmationMailRequest{ToAddr: "test@test.com", Link: "http://test.link"}
	resp, err := server.SendCardConfirmationEmail(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Successful {
		t.Fatalf("expected Successful=true, got false")
	}
}

func TestSendCardConfirmationEmail_Fail(t *testing.T) {
	createFakeTemplate("test-templates/card_confirmation.html", t)
	defer cleanupTemplates(t)

	mock := &MockSender{ShouldFail: true}
	server := &Server{sender: mock}

	req := &notification.CardConfirmationMailRequest{ToAddr: "test@test.com", Link: "http://test.link"}
	resp, _ := server.SendCardConfirmationEmail(context.Background(), req)
	if resp.Successful {
		t.Fatalf("expected Successful=false, got true")
	}
}

func TestSendCardCreatedEmail_Success(t *testing.T) {
	createFakeTemplate("test-templates/card_created.html", t)
	defer cleanupTemplates(t)

	mock := &MockSender{ShouldFail: false}
	server := &Server{sender: mock}

	req := &notification.CardCreatedMailRequest{ToAddr: "test@test.com"}
	resp, err := server.SendCardCreatedEmail(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Successful {
		t.Errorf("expected Successful=true, got false")
	}
}

func TestSendCardCreatedEmail_Fail(t *testing.T) {
	createFakeTemplate("test-templates/card_created.html", t)
	defer cleanupTemplates(t)

	mock := &MockSender{ShouldFail: true}
	server := &Server{sender: mock}

	req := &notification.CardCreatedMailRequest{ToAddr: "test@test.com"}
	resp, _ := server.SendCardCreatedEmail(context.Background(), req)
	if resp.Successful {
		t.Errorf("expected Successful=false, got true")
	}
}

func setupTestTemplates(t *testing.T) string {
	t.Helper()
	// Create a temporary directory
	tmpDir := t.TempDir()

	// The Server code expects "templates/xxx.html" relative to the working dir.
	// We'll create the templates folder inside our temp dir and change the working directory.
	templateDir := filepath.Join(tmpDir, "templates")
	err := os.MkdirAll(templateDir, 0755)
	if err != nil {
		t.Fatalf("failed to create templates dir: %v", err)
	}

	files := []string{
		"confirmation.html",
		"activation.html",
		"password_reset.html",
		"initial_password_set.html",
		"card_confirmation.html",
		"loan_payment_failed.html",
		"card_created.html",
	}

	for _, f := range files {
		content := []byte("<html><body>{{.}}</body></html>")
		err := os.WriteFile(filepath.Join(templateDir, f), content, 0644)
		if err != nil {
			t.Fatalf("failed to write template %s: %v", f, err)
		}
	}

	// Change working directory to the temp root so "templates/..." paths work
	oldWd, _ := os.Getwd()
	_ = os.Chdir(tmpDir)

	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	return tmpDir
}

// --- Tests ---

func TestNotificationServer_SuccessFlows(t *testing.T) {
	setupTestTemplates(t)
	server := NewServer(&MockSender{ShouldFail: false})
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func() (*notification.SuccessResponse, error)
	}{
		{
			"ConfirmationEmail",
			func() (*notification.SuccessResponse, error) {
				return server.SendConfirmationEmail(ctx, &notification.ConfirmationMailRequest{ToAddr: "a@b.com"})
			},
		},
		{
			"ActivationEmail",
			func() (*notification.SuccessResponse, error) {
				return server.SendActivationEmail(ctx, &notification.ActivationMailRequest{ToAddr: "a@b.com"})
			},
		},
		{
			"PasswordResetEmail",
			func() (*notification.SuccessResponse, error) {
				return server.SendPasswordResetEmail(ctx, &notification.PasswordLinkMailRequest{ToAddr: "a@b.com"})
			},
		},
		{
			"LoanPaymentFailedEmail",
			func() (*notification.SuccessResponse, error) {
				return server.SendLoanPaymentFailedEmail(ctx, &notification.LoanPaymentFailedMailRequest{
					ToAddr: "a@b.com", LoanNumber: "123", Amount: "100", Currency: "RSD", DueDate: "today",
				})
			},
		},
		{
			"CardCreatedEmail",
			func() (*notification.SuccessResponse, error) {
				return server.SendCardCreatedEmail(ctx, &notification.CardCreatedMailRequest{ToAddr: "a@b.com"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := tt.fn()
			if err != nil {
				t.Errorf("expected no error, got %v", err)
			}
			if resp == nil || !resp.Successful {
				t.Errorf("expected Successful=true, got false")
			}
		})
	}
}

func TestNotificationServer_SenderFailures(t *testing.T) {
	setupTestTemplates(t)
	server := NewServer(&MockSender{ShouldFail: true})
	ctx := context.Background()

	// Testing one representative method for sender failure
	resp, err := server.SendConfirmationEmail(ctx, &notification.ConfirmationMailRequest{ToAddr: "test@test.com"})

	if err != nil {
		t.Fatalf("RPC error should be nil even if email fails (based on your implementation)")
	}
	if resp.Successful {
		t.Error("expected Successful=false when sender fails")
	}
}

func TestNotificationServer_TemplateMissing(t *testing.T) {
	// Don't call setupTestTemplates or change to an empty dir
	t.Chdir(t.TempDir())

	server := NewServer(&MockSender{ShouldFail: false})
	resp, _ := server.SendConfirmationEmail(context.Background(), &notification.ConfirmationMailRequest{ToAddr: "test@test.com"})

	if resp.Successful {
		t.Error("expected Successful=false when template is missing")
	}
}

func TestSMTPSender_Manual(t *testing.T) {
	t.Setenv("FROM_EMAIL_AUTH", "user")
	t.Setenv("FROM_EMAIL_PASSWORD", "pass")
	t.Setenv("FROM_EMAIL_SMTP", "smtp.test.com")
	t.Setenv("FROM_EMAIL", "test@test.com")
	t.Setenv("SMTP_ADDR", "localhost:1234")

	sender := &SMTPSender{}
	err := sender.Send([]string{"to@to.com"}, "Subject", "Body")
	if err == nil {
		t.Error("expected error connecting to fake SMTP address")
	}
}
