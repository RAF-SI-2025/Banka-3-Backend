package app

import (
	"context"

	notifpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/notification/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// notifierAdapter implements service.Notifier on top of the
// notification-svc gRPC client. In-app rows go through CreateNotification;
// email goes through SendEmail. Both share the one notification client
// dialed in app.go (also used by notifEmailSender for OTC mail).
type notifierAdapter struct {
	c notifpb.NotificationServiceClient
}

func (n *notifierAdapter) InApp(ctx context.Context, userID string, kind domain.UserKind, eventKind, title, body string) error {
	_, err := n.c.CreateNotification(ctx, &notifpb.CreateNotificationRequest{
		UserId:   userID,
		UserKind: string(kind),
		Kind:     eventKind,
		Title:    title,
		Body:     body,
	})
	return err
}

func (n *notifierAdapter) Email(ctx context.Context, to, subject, body string) error {
	if to == "" {
		return nil
	}
	_, err := n.c.SendEmail(ctx, &notifpb.SendEmailRequest{
		To:            to,
		Subject:       subject,
		Body:          body,
		Kind:          notifpb.EmailKind_EMAIL_KIND_GENERIC,
		OriginService: "trading",
	})
	return err
}
