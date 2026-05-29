package service

import (
	"context"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/account"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// CreatePaymentRecipient stores a primalac plaćanja template owned by
// the calling client. Validates the 18-digit number's checksum so
// users can't save garbage.
func (s *Service) CreatePaymentRecipient(ctx context.Context, name, accountNumber string) (*domain.PaymentRecipient, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if p.UserKind != auth.KindClient {
		return nil, apperr.PermissionDenied("samo klijent može da čuva primaoce")
	}
	name = strings.TrimSpace(name)
	accountNumber = strings.TrimSpace(accountNumber)
	if name == "" {
		return nil, apperr.Validation("naziv primaoca je obavezan")
	}
	if _, err := account.Validate(accountNumber); err != nil {
		return nil, apperr.Validation("broj računa nije ispravan: " + err.Error())
	}
	return s.Store.UpsertPaymentRecipient(ctx, &domain.PaymentRecipient{
		ClientID: p.UserID, Name: name, AccountNumber: accountNumber,
	})
}

func (s *Service) ListPaymentRecipients(ctx context.Context) ([]*domain.PaymentRecipient, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if p.UserKind != auth.KindClient {
		// Employees don't have personal recipient lists; return empty
		// rather than error.
		return nil, nil
	}
	return s.Store.ListPaymentRecipients(ctx, p.UserID)
}

func (s *Service) UpdatePaymentRecipient(ctx context.Context, id, name, accountNumber string) (*domain.PaymentRecipient, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if p.UserKind != auth.KindClient {
		return nil, apperr.PermissionDenied("samo klijent može da menja primaoce")
	}
	if _, err := account.Validate(strings.TrimSpace(accountNumber)); err != nil {
		return nil, apperr.Validation("broj računa nije ispravan: " + err.Error())
	}
	return s.Store.UpdatePaymentRecipient(ctx, &domain.PaymentRecipient{
		ID: id, ClientID: p.UserID, Name: strings.TrimSpace(name), AccountNumber: strings.TrimSpace(accountNumber),
	})
}

func (s *Service) DeletePaymentRecipient(ctx context.Context, id string) error {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return err
	}
	if p.UserKind != auth.KindClient {
		return apperr.PermissionDenied("samo klijent može da briše primaoce")
	}
	return s.Store.DeletePaymentRecipient(ctx, id, p.UserID)
}
