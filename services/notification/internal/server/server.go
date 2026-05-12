// Package server adapts the proto-generated NotificationService surface
// to the service layer.
package server

import (
	"context"
	"log/slog"

	notifpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/notification/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/email"
)

// Server is the gRPC implementation of NotificationService. The service
// is a thin pass-through to pkg/email — for now templating still happens
// in the caller; this service just owns the SMTP credentials and tags
// outbound events with their kind for observability.
type Server struct {
	notifpb.UnimplementedNotificationServiceServer
	Sender email.Sender
	Log    *slog.Logger
}

func New(sender email.Sender, log *slog.Logger) *Server {
	return &Server{Sender: sender, Log: log}
}

// SendEmail dispatches one message. Idempotency is the caller's
// concern; this RPC is fire-and-forget at the SMTP layer.
func (s *Server) SendEmail(ctx context.Context, in *notifpb.SendEmailRequest) (*notifpb.SendEmailResponse, error) {
	if err := s.Sender.Send(ctx, email.Message{
		To:      in.GetTo(),
		Subject: in.GetSubject(),
		Body:    in.GetBody(),
		HTML:    in.GetHtml(),
	}); err != nil {
		s.Log.Warn("send email failed",
			"to", in.GetTo(), "kind", in.GetKind().String(),
			"origin", in.GetOriginService(), "err", err.Error())
		return nil, err
	}
	s.Log.Info("send email ok",
		"to", in.GetTo(), "kind", in.GetKind().String(),
		"origin", in.GetOriginService())
	return &notifpb.SendEmailResponse{}, nil
}

// Health is the slice-1 stub kept for the probe path.
func (s *Server) Health(ctx context.Context, _ *notifpb.HealthRequest) (*notifpb.HealthResponse, error) {
	return &notifpb.HealthResponse{Status: "ok"}, nil
}
