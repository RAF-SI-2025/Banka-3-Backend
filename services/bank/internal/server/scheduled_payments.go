package server

import (
	"context"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) SchedulePayment(ctx context.Context, in *bankpb.SchedulePaymentRequest) (*bankpb.ScheduledPayment, error) {
	sp, err := s.Svc.SchedulePayment(ctx, service.SchedulePaymentInput{
		FromAccountID:   in.GetFromAccountId(),
		ToAccountNumber: in.GetToAccountNumber(),
		Amount:          in.GetAmount(),
		RecipientName:   in.GetRecipientName(),
		PaymentCode:     in.GetPaymentCode(),
		ReferenceNumber: in.GetReferenceNumber(),
		Model:           in.GetModel(),
		Purpose:         in.GetPurpose(),
		ScheduledDate:   in.GetScheduledDate().AsTime(),
	})
	if err != nil {
		return nil, err
	}
	return scheduledPaymentToProto(sp), nil
}

func (s *Server) ListScheduledPayments(ctx context.Context, _ *bankpb.ListScheduledPaymentsRequest) (*bankpb.ListScheduledPaymentsResponse, error) {
	sps, err := s.Svc.ListScheduledPayments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.ScheduledPayment, 0, len(sps))
	for _, sp := range sps {
		out = append(out, scheduledPaymentToProto(sp))
	}
	return &bankpb.ListScheduledPaymentsResponse{ScheduledPayments: out}, nil
}

func (s *Server) CancelScheduledPayment(ctx context.Context, in *bankpb.CancelScheduledPaymentRequest) (*bankpb.ScheduledPayment, error) {
	sp, err := s.Svc.CancelScheduledPayment(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return scheduledPaymentToProto(sp), nil
}

func (s *Server) RunDueScheduledPayments(ctx context.Context, _ *bankpb.RunDueScheduledPaymentsRequest) (*bankpb.RunDueScheduledPaymentsResponse, error) {
	r, err := s.Svc.RunDueScheduledPayments(ctx)
	if err != nil {
		return nil, err
	}
	return &bankpb.RunDueScheduledPaymentsResponse{
		Processed: int32(r.Processed),
		Succeeded: int32(r.Succeeded),
		Failed:    int32(r.Failed),
	}, nil
}

func scheduledPaymentToProto(sp *domain.ScheduledPayment) *bankpb.ScheduledPayment {
	out := &bankpb.ScheduledPayment{
		Id:              sp.ID,
		ClientId:        sp.ClientID,
		FromAccountId:   sp.FromAccountID,
		ToAccountNumber: sp.ToAccountNumber,
		Amount:          sp.Amount,
		Currency:        currencyToProto(sp.Currency),
		RecipientName:   sp.RecipientName,
		PaymentCode:     sp.PaymentCode,
		Purpose:         sp.Purpose,
		Model:           sp.Model,
		ReferenceNumber: sp.ReferenceNumber,
		ScheduledDate:   timestamppb.New(sp.ScheduledDate),
		Status:          scheduledPaymentStatusToProto(sp.Status),
		FailureReason:   sp.FailureReason,
		CreatedAt:       timestamppb.New(sp.CreatedAt),
	}
	if sp.ExecutedAt != nil {
		out.ExecutedAt = timestamppb.New(*sp.ExecutedAt)
	}
	return out
}

func scheduledPaymentStatusToProto(st domain.ScheduledPaymentStatus) bankpb.ScheduledPaymentStatus {
	switch st {
	case domain.ScheduledPaymentScheduled:
		return bankpb.ScheduledPaymentStatus_SCHEDULED_PAYMENT_STATUS_SCHEDULED
	case domain.ScheduledPaymentCompleted:
		return bankpb.ScheduledPaymentStatus_SCHEDULED_PAYMENT_STATUS_COMPLETED
	case domain.ScheduledPaymentFailed:
		return bankpb.ScheduledPaymentStatus_SCHEDULED_PAYMENT_STATUS_FAILED
	case domain.ScheduledPaymentCancelled:
		return bankpb.ScheduledPaymentStatus_SCHEDULED_PAYMENT_STATUS_CANCELLED
	}
	return bankpb.ScheduledPaymentStatus_SCHEDULED_PAYMENT_STATUS_UNSPECIFIED
}
