// gRPC handler for CrossBankPaymentService — thin proto ↔ service
// adapter. Service-layer SubmitCrossBankPayment + GetCrossBankPayment
// do the work.

package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/schedule"
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

// --- Retry queue ---

// ListInterbankRetries handles GET /api/v1/payments/interbank/retries.
func (s *Server) ListInterbankRetries(ctx context.Context, _ *tradingpb.ListInterbankRetriesRequest) (*tradingpb.ListInterbankRetriesResponse, error) {
	rows, err := s.Svc.ListInterbankRetries(ctx)
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListInterbankRetriesResponse{Entries: make([]*tradingpb.InterbankRetryEntry, 0, len(rows))}
	for _, r := range rows {
		out.Entries = append(out.Entries, interbankRetryToProto(r))
	}
	return out, nil
}

// RunInterbankRetryTick re-drives every due retry entry. Internal-only
// RPC driven by the scheduler service.
func (s *Server) RunInterbankRetryTick(ctx context.Context, _ *tradingpb.RunInterbankRetryTickRequest) (*tradingpb.RunInterbankRetryTickResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	n, err := s.Svc.RunInterbankRetryTick(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunInterbankRetryTickResponse{Settled: int32(n)}, nil
}

func interbankRetryToProto(r *domain.InterbankRetryEntry) *tradingpb.InterbankRetryEntry {
	if r == nil {
		return nil
	}
	return &tradingpb.InterbankRetryEntry{
		Id:              r.ID,
		TransactionId:   r.TransactionID,
		PartnerBankCode: r.PartnerBankCode,
		Operation:       r.Operation,
		AttemptCount:    r.AttemptCount,
		NextRetryAt:     timestamppb.New(r.NextRetryAt),
		DeadlineAt:      timestamppb.New(r.DeadlineAt),
		Status:          string(r.Status),
		LastError:       r.LastError,
		CreatedAt:       timestamppb.New(r.CreatedAt),
		UpdatedAt:       timestamppb.New(r.UpdatedAt),
	}
}

// --- Scheduled / periodic inter-bank payments ---

// CreateScheduledInterbankPayment handles POST /api/v1/cross-bank-payments/scheduled.
func (s *Server) CreateScheduledInterbankPayment(ctx context.Context, in *tradingpb.CreateScheduledInterbankPaymentRequest) (*tradingpb.ScheduledInterbankPayment, error) {
	p, err := s.Svc.CreateScheduledInterbankPayment(ctx, service.CreateScheduledInterbankPaymentInput{
		SourceAccountID:   in.GetSourceAccountId(),
		DestBankCode:      in.GetDestBankCode(),
		DestAccountNumber: in.GetDestAccountNumber(),
		Currency:          currencyFromProto(in.GetCurrency()),
		Amount:            in.GetAmount(),
		Purpose:           in.GetPurpose(),
		Cadence:           schedule.Cadence(in.GetCadence()),
		StartDate:         in.GetStartDate(),
	})
	if err != nil {
		return nil, err
	}
	return scheduledInterbankToProto(p), nil
}

// ListScheduledInterbankPayments handles GET /api/v1/cross-bank-payments/scheduled.
func (s *Server) ListScheduledInterbankPayments(ctx context.Context, _ *tradingpb.ListScheduledInterbankPaymentsRequest) (*tradingpb.ListScheduledInterbankPaymentsResponse, error) {
	rows, err := s.Svc.ListScheduledInterbankPayments(ctx)
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListScheduledInterbankPaymentsResponse{
		ScheduledPayments: make([]*tradingpb.ScheduledInterbankPayment, 0, len(rows)),
	}
	for _, r := range rows {
		out.ScheduledPayments = append(out.ScheduledPayments, scheduledInterbankToProto(r))
	}
	return out, nil
}

// PauseScheduledInterbankPayment handles POST .../scheduled/{id}/pause.
func (s *Server) PauseScheduledInterbankPayment(ctx context.Context, in *tradingpb.PauseScheduledInterbankPaymentRequest) (*tradingpb.ScheduledInterbankPayment, error) {
	p, err := s.Svc.PauseScheduledInterbankPayment(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return scheduledInterbankToProto(p), nil
}

// ResumeScheduledInterbankPayment handles POST .../scheduled/{id}/resume.
func (s *Server) ResumeScheduledInterbankPayment(ctx context.Context, in *tradingpb.ResumeScheduledInterbankPaymentRequest) (*tradingpb.ScheduledInterbankPayment, error) {
	p, err := s.Svc.ResumeScheduledInterbankPayment(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return scheduledInterbankToProto(p), nil
}

// CancelScheduledInterbankPayment handles DELETE .../scheduled/{id}.
func (s *Server) CancelScheduledInterbankPayment(ctx context.Context, in *tradingpb.CancelScheduledInterbankPaymentRequest) (*tradingpb.CancelScheduledInterbankPaymentResponse, error) {
	if err := s.Svc.CancelScheduledInterbankPayment(ctx, in.GetId()); err != nil {
		return nil, err
	}
	return &tradingpb.CancelScheduledInterbankPaymentResponse{}, nil
}

// RunDueInterbankPayments submits every due scheduled payment.
// Internal-only RPC driven by the scheduler service.
func (s *Server) RunDueInterbankPayments(ctx context.Context, _ *tradingpb.RunDueInterbankPaymentsRequest) (*tradingpb.RunDueInterbankPaymentsResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	n, err := s.Svc.RunDueInterbankPayments(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunDueInterbankPaymentsResponse{Submitted: int32(n)}, nil
}

func scheduledInterbankToProto(p *domain.ScheduledInterbankPayment) *tradingpb.ScheduledInterbankPayment {
	if p == nil {
		return nil
	}
	out := &tradingpb.ScheduledInterbankPayment{
		Id:                p.ID,
		UserId:            p.UserID,
		SourceAccountId:   p.SourceAccountID,
		DestBankCode:      p.DestBankCode,
		DestAccountNumber: p.DestAccountNumber,
		Currency:          currencyToProto(p.Currency),
		Amount:            p.Amount,
		Purpose:           p.Purpose,
		Cadence:           p.Cadence,
		NextRun:           timestamppb.New(p.NextRun),
		Active:            p.Active,
		LastStatus:        p.LastStatus,
		LastError:         p.LastError,
		CreatedAt:         timestamppb.New(p.CreatedAt),
		UpdatedAt:         timestamppb.New(p.UpdatedAt),
	}
	if p.LastRunAt != nil {
		out.LastRunAt = timestamppb.New(*p.LastRunAt)
	}
	return out
}
