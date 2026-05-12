package app

import (
	"context"
	"fmt"

	notifpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/notification/v1"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/email"
)

// userResolverAdapter implements service.UserResolver on top of the
// user-service gRPC client. The bank service calls this with no user
// context (cron / event paths), so we pad outgoing metadata with the
// equivalent of an internal admin principal — same trust assumption as
// the gateway-to-service path documented in CLAUDE.md ("Services trust
// the gateway"; same applies to service-to-service over the cluster
// network).
type userResolverAdapter struct {
	c userpb.UserServiceClient
}

func (a *userResolverAdapter) ClientEmail(ctx context.Context, clientID string) (string, error) {
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "bank-service-internal",
		UserKind:    auth.KindEmployee,
		Permissions: []string{"admin", "client.read"},
	})
	resp, err := a.c.GetClient(ctx, &userpb.GetClientRequest{Id: clientID})
	if err != nil {
		return "", fmt.Errorf("user.GetClient: %w", err)
	}
	return resp.GetEmail(), nil
}

// bankNotifierAdapter is the email-sender side of the Notifier
// interface. Mirrors the user service's notifierAdapter to keep both
// services on the same shape until c2/c3 centralize email through the
// notification service.
type bankNotifierAdapter struct{ sender email.Sender }

func (n bankNotifierAdapter) Send(ctx context.Context, to, subject, body string, html bool) error {
	return n.sender.Send(ctx, email.Message{To: to, Subject: subject, Body: body, HTML: html})
}

// notifClientAdapter dials notification-svc.SendEmail (c4 PR4 NOTIFY-1).
// Templating still happens in bank-svc (the Serbian bodies are built in
// notifications.go); this adapter only hands the rendered message to
// the centralized dispatcher so SMTP credentials live in one place.
type notifClientAdapter struct {
	c notifpb.NotificationServiceClient
}

func (n *notifClientAdapter) Send(ctx context.Context, to, subject, body string, html bool) error {
	_, err := n.c.SendEmail(ctx, &notifpb.SendEmailRequest{
		To:            to,
		Subject:       subject,
		Body:          body,
		Html:          html,
		Kind:          notifpb.EmailKind_EMAIL_KIND_GENERIC,
		OriginService: "bank",
	})
	return err
}
