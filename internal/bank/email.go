package bank

import (
	"context"
	"log"

	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
)

func (s *Server) sendCardCreatedEmail(ctx context.Context, email string) error {
	log.Printf("[NotificationClient] Sending CardCreated email to: %s", email)

	_, err := s.NotificationService.SendCardCreatedEmail(ctx, &notificationpb.CardCreatedMailRequest{
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
	log.Printf("[NotificationClient] Sending LoanPaymentFailed email to: %s", email)

	_, err := s.NotificationService.SendLoanPaymentFailedEmail(ctx, &notificationpb.LoanPaymentFailedMailRequest{
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
	log.Printf("[NotificationClient] Sending CardConfirmation email to: %s", email)

	_, err := s.NotificationService.SendCardConfirmationEmail(ctx, &notificationpb.CardConfirmationMailRequest{
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

func (s *Server) sendLoanRequestCreatedEmail(ctx context.Context, email string) {
	if s.NotificationService == nil {
		log.Printf("[NotificationClient] WARNING: Notification client is not configured, skipping LoanRequestCreated email for %s", email)
		return
	}

	log.Printf("[NotificationClient] Attempting to send LoanRequestCreated email to: %s", email)

	resp, err := s.NotificationService.SendLoanRequestCreatedEmail(ctx, &notificationpb.LoanRequestCreatedMailRequest{
		ToAddr: email,
	})
	if err != nil {
		log.Printf("[NotificationClient] ERROR: Failed to call SendLoanRequestCreatedEmail for %s: %v", email, err)
		return
	}
	if !resp.Successful {
		log.Printf("[NotificationClient] ERROR: Notification service reported unsuccessful LoanRequestCreated email for %s", email)
		return
	}

	log.Printf("[NotificationClient] SUCCESS: LoanRequestCreated email sent to %s", email)
}

func (s *Server) sendCardBlockedEmail(ctx context.Context, email string, isBlocked bool) error {
	log.Printf("[NotificationClient] Sending CardBlocked email to: %s (Status: %v)", email, isBlocked)

	_, err := s.NotificationService.SendCardBlockedEmail(ctx, &notificationpb.CardBlockedReqest{
		ToAddr:    email,
		IsBlocked: isBlocked,
	})

	if err != nil {
		log.Printf("[NotificationClient] ERROR: Failed to call SendCardBlockedEmail for %s: %v", email, err)
		return err
	}

	log.Printf("[NotificationClient] SUCCESS: CardBlocked email sent to %s", email)
	return nil
}
