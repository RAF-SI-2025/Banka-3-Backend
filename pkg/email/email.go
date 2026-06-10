// Package email sends transactional emails. In production it dials an
// SMTP server (config from SMTP_HOST/SMTP_PORT/SMTP_USERNAME/SMTP_PASSWORD/
// SMTP_FROM); when SMTP_HOST is unset it logs the full message to stdout
// instead — useful in dev and CI without changing any code.
package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
	"time"
)

// Message is a single email to send. HTML toggles the Content-Type.
type Message struct {
	To      string
	Subject string
	Body    string
	HTML    bool
}

// Sender sends emails. Multiple implementations live below.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Config holds SMTP connection settings. Empty Host selects [LogSender].
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	UseTLS   bool
}

// New picks an implementation based on cfg. If cfg.Host is empty the
// returned sender writes messages to log instead of dialing SMTP.
func New(cfg Config, log *slog.Logger) Sender {
	if cfg.Host == "" {
		log.Info("email: no SMTP_HOST set, using log sender")
		return &LogSender{Log: log}
	}
	return &SMTPSender{Cfg: cfg, Log: log}
}

// LogSender prints messages to log at info level. Used in dev/CI.
type LogSender struct{ Log *slog.Logger }

// Send implements [Sender].
func (s *LogSender) Send(_ context.Context, m Message) error {
	s.Log.Info("email (logged, not sent)",
		"to", m.To,
		"subject", m.Subject,
		"body", m.Body,
		"html", m.HTML,
	)
	return nil
}

// SMTPSender dials Cfg.Host:Cfg.Port and sends with optional STARTTLS.
type SMTPSender struct {
	Cfg Config
	Log *slog.Logger
}

// Send implements [Sender]. Caller's context bounds the connection.
func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	addr := fmt.Sprintf("%s:%d", s.Cfg.Host, s.Cfg.Port)

	contentType := "text/plain; charset=UTF-8"
	if m.HTML {
		contentType = "text/html; charset=UTF-8"
	}
	headers := []string{
		"From: " + s.Cfg.From,
		"To: " + m.To,
		"Subject: " + m.Subject,
		"MIME-Version: 1.0",
		"Content-Type: " + contentType,
	}
	body := strings.Join(headers, "\r\n") + "\r\n\r\n" + m.Body

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		var auth smtp.Auth
		if s.Cfg.Username != "" {
			auth = smtp.PlainAuth("", s.Cfg.Username, s.Cfg.Password, s.Cfg.Host)
		}
		if s.Cfg.UseTLS {
			done <- result{s.sendTLS(addr, auth, m.To, []byte(body))}
			return
		}
		done <- result{smtp.SendMail(addr, auth, s.Cfg.From, []string{m.To}, []byte(body))}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			s.Log.ErrorContext(ctx, "smtp send failed", "err", r.err, "to", m.To, "subject", m.Subject)
			return fmt.Errorf("smtp send: %w", r.err)
		}
		s.Log.InfoContext(ctx, "email sent", "to", m.To, "subject", m.Subject)
		return nil
	case <-dialCtx.Done():
		s.Log.ErrorContext(ctx, "smtp send timed out", "err", dialCtx.Err(), "to", m.To, "subject", m.Subject)
		return fmt.Errorf("smtp send: %w", dialCtx.Err())
	}
}

func (s *SMTPSender) sendTLS(addr string, auth smtp.Auth, to string, body []byte) error {
	tlsConfig := &tls.Config{ServerName: s.Cfg.Host, MinVersion: tls.VersionTLS12}
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return err
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, s.Cfg.Host)
	if err != nil {
		return err
	}
	defer c.Quit()

	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(s.Cfg.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	return w.Close()
}
