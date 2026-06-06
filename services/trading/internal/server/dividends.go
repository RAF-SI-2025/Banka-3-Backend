package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ListDividendPayouts returns the caller's dividend history (S59),
// optionally scoped to one security.
func (s *Server) ListDividendPayouts(ctx context.Context, in *tradingpb.ListDividendPayoutsRequest) (*tradingpb.ListDividendPayoutsResponse, error) {
	rows, err := s.Svc.ListDividendPayouts(ctx, service.ListDividendPayoutsInput{
		UserID:     in.GetUserId(),
		UserKind:   userKindFromProto(in.GetUserKind()),
		SecurityID: in.GetSecurityId(),
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListDividendPayoutsResponse{Payouts: make([]*tradingpb.DividendPayout, 0, len(rows))}
	for _, d := range rows {
		out.Payouts = append(out.Payouts, dividendPayoutToProto(d))
	}
	return out, nil
}

// RunDividendPayout is the scheduler-driven quarterly dividend cron
// (S54-S58). Internal-only.
func (s *Server) RunDividendPayout(ctx context.Context, _ *tradingpb.RunDividendPayoutRequest) (*tradingpb.RunDividendPayoutResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	r, err := s.Svc.RunDividendPayout(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunDividendPayoutResponse{
		Paid:     r.Paid,
		Skipped:  r.Skipped,
		TotalRsd: r.TotalRSD,
		Ran:      r.RanThisCall,
	}, nil
}

func dividendPayoutToProto(d *domain.DividendPayout) *tradingpb.DividendPayout {
	if d == nil {
		return nil
	}
	out := &tradingpb.DividendPayout{
		Id:          d.ID,
		UserId:      d.UserID,
		UserKind:    userKindToProto(d.UserKind),
		SecurityId:  d.SecurityID,
		Quantity:    d.Quantity,
		Price:       d.Price,
		GrossAmount: d.GrossAmount,
		Currency:    currencyToProto(d.Currency),
		AccountId:   d.AccountID,
		TaxRsd:      d.TaxRSD,
		Status:      d.Status,
		CreatedAt:   timestamppb.New(d.CreatedAt),
	}
	if d.PaidAt != nil {
		out.PaidAt = timestamppb.New(*d.PaidAt)
	}
	return out
}
