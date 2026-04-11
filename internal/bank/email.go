package bank

import (
	"context"
	"log"
	"os"

	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
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

	log.Printf("[NotificationClient] Attempting to send CardCreated email to: %s via %s", email, addr)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("[NotificationClient] ERROR: Failed to create gRPC client for %s: %v", addr, err)
		return err
	}
	defer func(conn *grpc.ClientConn) {
		err := conn.Close()
		if err != nil {
			log.Printf("[NotificationClient] ERROR: Failed to close gRPC connection to %s: %v", addr, err)
		}
	}(conn)

	client := notificationpb.NewNotificationServiceClient(conn)
	_, err = client.SendCardCreatedEmail(ctx, &notificationpb.CardCreatedMailRequest{
		ToAddr: email,
	})

	if err != nil {
		log.Printf("[NotificationClient] ERROR: Failed to call SendCardCreatedEmail for %s: %v", email, err)
		return err
	}

	log.Printf("[NotificationClient] SUCCESS: CardCreated email sent to %s", email)
	return nil
}

func (s *Server) sendLoanPaymentFailedEmail(ctx context.Context, email, loanNumber, amount, currency, dueDate string) error {
	addr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if addr == "" {
		addr = defaultNotificationURL
	}

	log.Printf("[NotificationClient] Attempting to send LoanPaymentFailed email to: %s via %s", email, addr)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("[NotificationClient] ERROR: Failed to create gRPC client for %s: %v", addr, err)
		return err
	}
	defer func(conn *grpc.ClientConn) {
		err := conn.Close()
		if err != nil {
			log.Printf("[NotificationClient] ERROR: Failed to close gRPC connection to %s: %v", addr, err)
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
		log.Printf("[NotificationClient] ERROR: Failed to call SendLoanPaymentFailedEmail for %s: %v", email, err)
		return err
	}

	log.Printf("[NotificationClient] SUCCESS: LoanPaymentFailed email sent to %s", email)
	return nil
}

func (s *Server) sendCardConfirmationEmail(ctx context.Context, email string, link string) error {
	addr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if addr == "" {
		addr = defaultNotificationURL
	}

	log.Printf("[NotificationClient] Attempting to send CardConfirmation email to: %s (Link: %s) via %s", email, link, addr)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("[NotificationClient] ERROR: Failed to create gRPC client for %s: %v", addr, err)
		return err
	}
	defer func(conn *grpc.ClientConn) {
		err := conn.Close()
		if err != nil {
			log.Printf("[NotificationClient] ERROR: Failed to close gRPC connection to %s: %v", addr, err)
		}
	}(conn)

	client := notificationpb.NewNotificationServiceClient(conn)
	_, err = client.SendCardConfirmationEmail(ctx, &notificationpb.CardConfirmationMailRequest{
		ToAddr: email,
		Link:   link,
	})

	if err != nil {
		log.Printf("[NotificationClient] ERROR: Failed to call SendCardConfirmationEmail for %s: %v", email, err)
		return err
	}

	log.Printf("[NotificationClient] SUCCESS: CardConfirmation email sent to %s", email)
	return nil
}
