package notification

import (
	"bytes"
	"context"
	"html/template"
	"log"
	"net/smtp"
	"os"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
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
	log.Println("Sending confirmation email")

	to := strings.Split(req.ToAddr, ",")

	templ, err := template.ParseFiles("templates/confirmation.html")
	if err != nil {
		log.Println("Cannot parse confirmation.html:", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, req); err != nil {
		log.Println("Cannot execute confirmation.html:", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Confirm your Banka 3 account", rendered.String()) //umesto smtp
	if err != nil {
		log.Println("Couldn't send confirmation email:", err)
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
		log.Println("Cannot parse activation.html:", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, req); err != nil {
		log.Println("Cannot execute activation.html:", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Aktivirajte Banka 3 nalog", rendered.String()) //umesto smtp
	if err != nil {
		log.Println("Couldn't send email:", err)
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
		log.Println("Cannot parse password_reset.html:", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, req); err != nil {
		log.Println("Cannot execute password_reset.html:", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Reset your Banka 3 password", rendered.String())
	if err != nil {
		log.Println("Couldn't send password reset email:", err)
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
		log.Println("Cannot parse initial_password_set.html:", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	var rendered bytes.Buffer
	if err := templ.Execute(&rendered, req); err != nil {
		log.Println("Cannot execute initial_password_set.html:", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	err = s.sender.Send(to, "Set your Banka 3 password", rendered.String())
	if err != nil {
		log.Println("Couldn't send initial password set email:", err)
		return &notification.SuccessResponse{Successful: false}, nil
	}

	return &notification.SuccessResponse{
		Successful: true,
	}, nil
}
