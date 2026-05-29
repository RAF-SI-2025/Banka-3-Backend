// gRPC handler for CrossBankPaymentService — thin proto ↔ service
// adapter. Service-layer SubmitCrossBankPayment + GetCrossBankPayment
// do the work.

package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// SubmitCrossBankPayment handles POST /api/v1/payments/interbank.
func (s *Server) SubmitCrossBankPayment(ctx context.Context, in *tradingpb.SubmitCrossBankPaymentRequest) (*tradingpb.SubmitCrossBankPaymentResponse, error) {
	res, err := s.Svc.SubmitCrossBankPayment(ctx, service.SubmitCrossBankPaymentInput{
		IdempotencyKey:      in.GetIdempotencyKey(),
		SourceAccountID:     in.GetSourceAccountId(),
		RemoteBankCode:      in.GetRemoteBankCode(),
		RemoteAccountNumber: in.GetRemoteAccountNumber(),
		Currency:            currencyFromProto(in.GetCurrency()),
		Amount:              in.GetAmount(),
		Purpose:             in.GetPurpose(),
	})
	if err != nil {
		return nil, err
	}
	return &tradingpb.SubmitCrossBankPaymentResponse{
		TransactionId: res.TransactionID,
		Status:        res.Status,
		LastError:     res.LastError,
	}, nil
}

// GetCrossBankPayment handles GET /api/v1/payments/interbank/{transaction_id}.
func (s *Server) GetCrossBankPayment(ctx context.Context, in *tradingpb.GetCrossBankPaymentRequest) (*tradingpb.CrossBankPayment, error) {
	v, err := s.Svc.GetCrossBankPayment(ctx, in.GetTransactionId())
	if err != nil {
		return nil, err
	}
	return &tradingpb.CrossBankPayment{
		TransactionId:       v.TransactionID,
		Status:              v.Status,
		CurrentStep:         v.CurrentStep,
		Attempts:            int32(v.Attempts),
		LastError:           v.LastError,
		CreatedAt:           timestamppb.New(v.CreatedAt),
		UpdatedAt:           timestamppb.New(v.UpdatedAt),
		SourceAccountId:     v.SourceAccountID,
		SourceAccountNumber: v.SourceAccountNumber,
		RemoteBankCode:      v.RemoteBankCode,
		RemoteAccountNumber: v.RemoteAccountNumber,
		Currency:            currencyToProto(domain.Currency(v.Currency)),
		Amount:              v.Amount,
		Purpose:             v.Purpose,
	}, nil
}
