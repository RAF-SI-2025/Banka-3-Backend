package app

import (
	"context"

	notifpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/notification/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/email"
)

// notifEmailSender implements email.Sender by dialing notification-svc
// (c4 PR4 NOTIFY-1). Trading's OTC notifier still composes the Serbian
// templates locally; this sender hands the rendered message to the
// centralized dispatcher so SMTP credentials live in notification-svc
// rather than every service.
type notifEmailSender struct {
	c notifpb.NotificationServiceClient
}

func (s *notifEmailSender) Send(ctx context.Context, m email.Message) error {
	_, err := s.c.SendEmail(ctx, &notifpb.SendEmailRequest{
		To:            m.To,
		Subject:       m.Subject,
		Body:          m.Body,
		Html:          m.HTML,
		Kind:          notifpb.EmailKind_EMAIL_KIND_GENERIC,
		OriginService: "trading",
	})
	return err
}
