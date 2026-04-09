package bank

import (
	"context"
	"os"

	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultNotificationURL = "notification:50051"
)

func (s *Server) sendCardCreatedEmail(ctx context.Context, email string) error {
	addr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if addr == "" {
		addr = defaultNotificationURL
	}

	l := logger.FromContext(ctx).With("notification", "CardCreated", "to", email, "addr", addr)
	l.Info("sending notification email")

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(logger.UnaryClientInterceptor()))
	if err != nil {
		l.Error("failed to create grpc client", "err", err)
		return err
	}
	defer func(conn *grpc.ClientConn) {
		if err := conn.Close(); err != nil {
			l.Error("failed to close grpc connection", "err", err)
		}
	}(conn)

	client := notificationpb.NewNotificationServiceClient(conn)
	_, err = client.SendCardCreatedEmail(ctx, &notificationpb.CardCreatedMailRequest{
		ToAddr: email,
	})

	if err != nil {
		l.Error("SendCardCreatedEmail failed", "err", err)
		return err
	}

	l.Info("notification email sent")
	return nil
}

func (s *Server) sendLoanPaymentFailedEmail(ctx context.Context, email, loanNumber, amount, currency, dueDate string) error {
	addr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if addr == "" {
		addr = defaultNotificationURL
	}

	l := logger.FromContext(ctx).With("notification", "LoanPaymentFailed", "to", email, "addr", addr, "loan", loanNumber)
	l.Info("sending notification email")

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(logger.UnaryClientInterceptor()))
	if err != nil {
		l.Error("failed to create grpc client", "err", err)
		return err
	}
	defer func(conn *grpc.ClientConn) {
		if err := conn.Close(); err != nil {
			l.Error("failed to close grpc connection", "err", err)
		}
	}(conn)

	client := notificationpb.NewNotificationServiceClient(conn)
	_, err = client.SendLoanPaymentFailedEmail(ctx, &notificationpb.LoanPaymentFailedMailRequest{
		ToAddr:     email,
		LoanNumber: loanNumber,
		Amount:     amount,
		Currency:   currency,
		DueDate:    dueDate,
	})

	if err != nil {
		l.Error("SendLoanPaymentFailedEmail failed", "err", err)
		return err
	}

	l.Info("notification email sent")
	return nil
}

func (s *Server) sendCardConfirmationEmail(ctx context.Context, email string, link string) error {
	addr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if addr == "" {
		addr = defaultNotificationURL
	}

	l := logger.FromContext(ctx).With("notification", "CardConfirmation", "to", email, "addr", addr, "link", link)
	l.Info("sending notification email")

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(logger.UnaryClientInterceptor()))
	if err != nil {
		l.Error("failed to create grpc client", "err", err)
		return err
	}
	defer func(conn *grpc.ClientConn) {
		if err := conn.Close(); err != nil {
			l.Error("failed to close grpc connection", "err", err)
		}
	}(conn)

	client := notificationpb.NewNotificationServiceClient(conn)
	_, err = client.SendCardConfirmationEmail(ctx, &notificationpb.CardConfirmationMailRequest{
		ToAddr: email,
		Link:   link,
	})

	if err != nil {
		l.Error("SendCardConfirmationEmail failed", "err", err)
		return err
	}

	l.Info("notification email sent")
	return nil
}
