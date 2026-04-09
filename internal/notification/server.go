package notification

import (
	"bytes"
	"context"
	"html/template"
	"net/smtp"
	"os"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
)

type EmailSender interface {
	Send(to []string, subject string, body string) error
}

type SMTPSender struct{}

func (s *SMTPSender) Send(to []string, subject string, body string) error {
	auth := smtp.PlainAuth(
		"",
		os.Getenv("FROM_EMAIL_AUTH"),
		os.Getenv("FROM_EMAIL_PASSWORD"),
		os.Getenv("FROM_EMAIL_SMTP"),
	)
	headers := []string{
		"From: " + os.Getenv("FROM_EMAIL"),
		"To: " + strings.Join(to, ","),
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=\"UTF-8\"",
	}
	message := strings.Join(headers, "\r\n") + "\r\n" + body
	return smtp.SendMail(os.Getenv("SMTP_ADDR"), auth, os.Getenv("FROM_EMAIL"), to, []byte(message))
}

type Server struct {
	notification.UnimplementedNotificationServiceServer
	sender EmailSender
}

func NewServer(sender EmailSender) *Server {
	return &Server{sender: sender}
}

func (s *Server) SendConfirmationEmail(ctx context.Context, req *notification.ConfirmationMailRequest) (*notification.SuccessResponse, error) {
	logger.FromContext(ctx).InfoContext(ctx, "sending confirmation email")

	to := strings.Split(req.ToAddr, ",")

	templ, err := template.ParseFiles("templates/confirmation.html")
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "parse confirmation.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, req); err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "execute confirmation.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Confirm your Banka 3 account", rendered.String()) //umesto smtp
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "send confirmation email failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	return &notification.SuccessResponse{
		Successful: true,
	}, nil
}

func (s *Server) SendActivationEmail(ctx context.Context, req *notification.ActivationMailRequest) (*notification.SuccessResponse, error) {
	to := strings.Split(req.ToAddr, ",")
	templ, err := template.ParseFiles("templates/activation.html")
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "parse activation.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, req); err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "execute activation.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Aktivirajte Banka 3 nalog", rendered.String()) //umesto smtp
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "send activation email failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	return &notification.SuccessResponse{
		Successful: true,
	}, nil
}

func (s *Server) SendPasswordResetEmail(ctx context.Context, req *notification.PasswordLinkMailRequest) (*notification.SuccessResponse, error) {
	to := strings.Split(req.ToAddr, ",")
	templ, err := template.ParseFiles("templates/password_reset.html")
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "parse password_reset.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, req); err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "execute password_reset.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Reset your Banka 3 password", rendered.String())
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "send password reset email failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	return &notification.SuccessResponse{
		Successful: true,
	}, nil
}

func (s *Server) SendInitialPasswordSetEmail(ctx context.Context, req *notification.PasswordLinkMailRequest) (*notification.SuccessResponse, error) {
	to := strings.Split(req.ToAddr, ",")
	templ, err := template.ParseFiles("templates/initial_password_set.html")
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "parse initial_password_set.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, req); err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "execute initial_password_set.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Set your Banka 3 password", rendered.String())
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "send initial password set email failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	return &notification.SuccessResponse{
		Successful: true,
	}, nil
}

func (s *Server) SendCardConfirmationEmail(ctx context.Context, req *notification.CardConfirmationMailRequest) (*notification.SuccessResponse, error) {
	to := strings.Split(req.ToAddr, ",")
	templ, err := template.ParseFiles("templates/card_confirmation.html")
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "parse card_confirmation.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	data := struct {
		Link string
	}{
		Link: req.Link,
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, data); err != nil {
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Potvrda zahteva za karticu - Banka 3", rendered.String())
	if err != nil {
		return &notification.SuccessResponse{Successful: false}, nil
	}

	return &notification.SuccessResponse{Successful: true}, nil
}

func (s *Server) SendLoanPaymentFailedEmail(ctx context.Context, req *notification.LoanPaymentFailedMailRequest) (*notification.SuccessResponse, error) {
	to := strings.Split(req.ToAddr, ",")
	templ, err := template.ParseFiles("templates/loan_payment_failed.html")
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "parse loan_payment_failed.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	data := struct {
		LoanNumber string
		Amount     string
		Currency   string
		DueDate    string
	}{
		LoanNumber: req.LoanNumber,
		Amount:     req.Amount,
		Currency:   req.Currency,
		DueDate:    req.DueDate,
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, data); err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "execute loan_payment_failed.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Neuspela naplata rate kredita - Banka 3", rendered.String())
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "send loan payment failed email failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	return &notification.SuccessResponse{Successful: true}, nil
}

func (s *Server) SendCardCreatedEmail(ctx context.Context, req *notification.CardCreatedMailRequest) (*notification.SuccessResponse, error) {
	to := strings.Split(req.ToAddr, ",")
	templ, err := template.ParseFiles("templates/card_created.html")
	if err != nil {
		return &notification.SuccessResponse{Successful: false}, nil
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, req); err != nil {
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Vaša Banka 3 kartica je spremna!", rendered.String())
	if err != nil {
		return &notification.SuccessResponse{Successful: false}, nil
	}

	return &notification.SuccessResponse{Successful: true}, nil
}

func (s *Server) SendTOTPDisableEmail(ctx context.Context, req *notification.SendTOTPDisableEmailRequest) (*notification.SuccessResponse, error) {
	to := strings.Split(req.Email, ",")
	templ, err := template.ParseFiles("templates/disable_totp.html")
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "parse disable_totp.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, req); err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "execute disable_totp.html failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Disable TOTP request", rendered.String())
	if err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "send disable totp email failed", "err", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}
	return &notification.SuccessResponse{Successful: true}, nil
}
