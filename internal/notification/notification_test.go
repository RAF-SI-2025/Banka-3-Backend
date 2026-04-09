package notification

import (
	"context"
	"errors"
	"os"
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

func TestSendLoanRequestCreatedEmail_Success(t *testing.T) {
	createFakeTemplate("test-templates/loan_request_created.html", t)
	defer cleanupTemplates(t)

	mock := &MockSender{ShouldFail: false}
	server := &Server{sender: mock}

	req := &notification.LoanRequestCreatedMailRequest{ToAddr: "test@test.com"}
	resp, err := server.SendLoanRequestCreatedEmail(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Successful {
		t.Errorf("expected Successful=true, got false")
	}
}

func TestSendLoanRequestCreatedEmail_Fail(t *testing.T) {
	createFakeTemplate("test-templates/loan_request_created.html", t)
	defer cleanupTemplates(t)

	mock := &MockSender{ShouldFail: true}
	server := &Server{sender: mock}

	req := &notification.LoanRequestCreatedMailRequest{ToAddr: "test@test.com"}
	resp, _ := server.SendLoanRequestCreatedEmail(context.Background(), req)
	if resp.Successful {
		t.Errorf("expected Successful=false, got true")
	}
}
