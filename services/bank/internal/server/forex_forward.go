package server

import (
	"context"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) QuoteForexForward(ctx context.Context, in *bankpb.QuoteForexForwardRequest) (*bankpb.ForexForwardQuote, error) {
	q, err := s.Svc.QuoteForexForward(ctx,
		currencyFromProto(in.GetBaseCurrency()),
		domain.CurrencyRSD,
		in.GetNotional(),
		in.GetSettlementDate().AsTime(),
	)
	if err != nil {
		return nil, err
	}
	return forexForwardQuoteToProto(q), nil
}

func (s *Server) CreateForexForward(ctx context.Context, in *bankpb.CreateForexForwardRequest) (*bankpb.ForexForward, error) {
	f, err := s.Svc.CreateForexForward(ctx, service.CreateForexForwardInput{
		BaseCurrency:   currencyFromProto(in.GetBaseCurrency()),
		Notional:       in.GetNotional(),
		SettlementDate: in.GetSettlementDate().AsTime(),
	})
	if err != nil {
		return nil, err
	}
	return forexForwardToProto(f), nil
}

func (s *Server) ListForexForwards(ctx context.Context, _ *bankpb.ListForexForwardsRequest) (*bankpb.ListForexForwardsResponse, error) {
	fs, err := s.Svc.ListForexForwards(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.ForexForward, 0, len(fs))
	for _, f := range fs {
		out = append(out, forexForwardToProto(f))
	}
	return &bankpb.ListForexForwardsResponse{ForexForwards: out}, nil
}

func (s *Server) CancelForexForward(ctx context.Context, in *bankpb.CancelForexForwardRequest) (*bankpb.ForexForward, error) {
	f, err := s.Svc.CancelForexForward(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return forexForwardToProto(f), nil
}

func (s *Server) GetForexForwardSpreads(ctx context.Context, _ *bankpb.GetForexForwardSpreadsRequest) (*bankpb.GetForexForwardSpreadsResponse, error) {
	sps, err := s.Svc.GetForexForwardSpreads(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.ForexForwardSpread, 0, len(sps))
	for _, sp := range sps {
		out = append(out, forexForwardSpreadToProto(sp))
	}
	return &bankpb.GetForexForwardSpreadsResponse{Spreads: out}, nil
}

func (s *Server) SetForexForwardSpread(ctx context.Context, in *bankpb.SetForexForwardSpreadRequest) (*bankpb.ForexForwardSpread, error) {
	sp, err := s.Svc.SetForexForwardSpread(ctx,
		currencyFromProto(in.GetBaseCurrency()),
		currencyFromProto(in.GetQuoteCurrency()),
		in.GetSpreadFactor(),
	)
	if err != nil {
		return nil, err
	}
	return forexForwardSpreadToProto(sp), nil
}

func (s *Server) RunForexForwardSettlement(ctx context.Context, _ *bankpb.RunForexForwardSettlementRequest) (*bankpb.RunForexForwardSettlementResponse, error) {
	r, err := s.Svc.RunForexForwardSettlement(ctx)
	if err != nil {
		return nil, err
	}
	return &bankpb.RunForexForwardSettlementResponse{
		Processed: int32(r.Processed),
		Settled:   int32(r.Settled),
		Failed:    int32(r.Failed),
	}, nil
}

func forexForwardToProto(f *domain.ForexForward) *bankpb.ForexForward {
	out := &bankpb.ForexForward{
		Id:               f.ID,
		ClientId:         f.ClientID,
		BaseCurrency:     currencyToProto(f.BaseCurrency),
		QuoteCurrency:    currencyToProto(f.QuoteCurrency),
		Notional:         f.Notional,
		ForwardRate:      f.ForwardRate,
		SpotAskRate:      f.SpotAskRate,
		SpreadFactor:     f.SpreadFactor,
		DaysToSettlement: int32(f.DaysToSettlement),
		Commission:       f.Commission,
		ReservationId:    f.ReservationID,
		FromAccountId:    f.FromAccountID,
		ToAccountId:      f.ToAccountID,
		SettlementDate:   timestamppb.New(f.SettlementDate),
		Status:           forexForwardStatusToProto(f.Status),
		FailureReason:    f.FailureReason,
		CreatedAt:        timestamppb.New(f.CreatedAt),
	}
	if f.SettledAt != nil {
		out.SettledAt = timestamppb.New(*f.SettledAt)
	}
	return out
}

func forexForwardSpreadToProto(sp *domain.ForexForwardSpread) *bankpb.ForexForwardSpread {
	return &bankpb.ForexForwardSpread{
		BaseCurrency:  currencyToProto(sp.BaseCurrency),
		QuoteCurrency: currencyToProto(sp.QuoteCurrency),
		SpreadFactor:  sp.SpreadFactor,
		UpdatedBy:     sp.UpdatedBy,
		UpdatedAt:     timestamppb.New(sp.UpdatedAt),
	}
}

func forexForwardQuoteToProto(q *service.ForexForwardQuote) *bankpb.ForexForwardQuote {
	return &bankpb.ForexForwardQuote{
		BaseCurrency:     currencyToProto(q.BaseCurrency),
		QuoteCurrency:    currencyToProto(q.QuoteCurrency),
		Notional:         q.Notional,
		SpotAskRate:      q.SpotAskRate,
		SpreadFactor:     q.SpreadFactor,
		DaysToSettlement: int32(q.DaysToSettlement),
		ForwardRate:      q.ForwardRate,
		QuoteAmount:      q.QuoteAmount,
		Commission:       q.Commission,
	}
}

func forexForwardStatusToProto(st domain.ForexForwardStatus) bankpb.ForexForwardStatus {
	switch st {
	case domain.ForexForwardActive:
		return bankpb.ForexForwardStatus_FOREX_FORWARD_STATUS_ACTIVE
	case domain.ForexForwardSettled:
		return bankpb.ForexForwardStatus_FOREX_FORWARD_STATUS_SETTLED
	case domain.ForexForwardCancelled:
		return bankpb.ForexForwardStatus_FOREX_FORWARD_STATUS_CANCELLED
	case domain.ForexForwardFailed:
		return bankpb.ForexForwardStatus_FOREX_FORWARD_STATUS_FAILED
	}
	return bankpb.ForexForwardStatus_FOREX_FORWARD_STATUS_UNSPECIFIED
}
