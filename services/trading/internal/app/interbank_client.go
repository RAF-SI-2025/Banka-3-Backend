// Celina 5 — bank InterbankProtocolService adapter.
//
// Trading dials bank's c5 2PC primitive (BE-5) for the cash legs of
// cross-bank OTC accept / exercise. Lives next to bank_client.go,
// shares the withBankAdmin metadata pattern.

package app

import (
	"context"
	"fmt"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

// interbankPayerAdapter wraps bank's InterbankProtocolService client
// and satisfies service.InterbankPayer.
type interbankPayerAdapter struct {
	c bankpb.InterbankProtocolServiceClient
}

func (a *interbankPayerAdapter) PreparePayment(ctx context.Context, in service.PrepareInterbankInput) (service.PrepareInterbankResult, error) {
	ctx = withBankAdmin(ctx)
	resp, err := a.c.PreparePayment(ctx, &bankpb.PreparePaymentRequest{
		SenderRoutingNumber: int32(in.SenderRoutingNumber),
		TransactionId:       in.TransactionID,
		// Outgoing only — we never call Prepare on the inbound side; that
		// happens when a partner reaches us via gateway → bank directly.
		Direction:           bankpb.InterbankPaymentDirection_INTERBANK_PAYMENT_DIRECTION_OUTBOUND,
		LocalAccountNumber:  in.LocalAccountNumber,
		RemoteAccountNumber: in.RemoteAccountNumber,
		Currency:            currencyToBankProto(in.Currency),
		Amount:              in.Amount,
		Purpose:             in.Purpose,
	})
	if err != nil {
		return service.PrepareInterbankResult{}, fmt.Errorf("bank.PreparePayment: %w", err)
	}
	return service.PrepareInterbankResult{
		TransactionID: resp.GetTransactionId(),
		ReservationID: resp.GetReservationId(),
		Status:        resp.GetStatus().String(),
	}, nil
}

func (a *interbankPayerAdapter) CommitPayment(ctx context.Context, senderRouting int, txID string) (service.CommitInterbankResult, error) {
	ctx = withBankAdmin(ctx)
	resp, err := a.c.CommitPayment(ctx, &bankpb.CommitPaymentRequest{
		SenderRoutingNumber: int32(senderRouting),
		TransactionId:       txID,
	})
	if err != nil {
		return service.CommitInterbankResult{}, fmt.Errorf("bank.CommitPayment: %w", err)
	}
	return service.CommitInterbankResult{
		TransactionID: resp.GetTransactionId(),
		OpID:          resp.GetOpId(),
		Status:        resp.GetStatus().String(),
	}, nil
}

func (a *interbankPayerAdapter) RollbackPayment(ctx context.Context, senderRouting int, txID, reason string) error {
	ctx = withBankAdmin(ctx)
	_, err := a.c.RollbackPayment(ctx, &bankpb.RollbackPaymentRequest{
		SenderRoutingNumber: int32(senderRouting),
		TransactionId:       txID,
		Reason:              reason,
	})
	if err != nil {
		return fmt.Errorf("bank.RollbackPayment: %w", err)
	}
	return nil
}
