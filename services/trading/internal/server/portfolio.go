package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) ListHoldings(ctx context.Context, in *tradingpb.ListHoldingsRequest) (*tradingpb.ListHoldingsResponse, error) {
	rows, totalProfit, err := s.Svc.ListHoldings(ctx, service.ListHoldingsInput{
		UserID:   in.GetUserId(),
		UserKind: userKindFromProto(in.GetUserKind()),
		Type:     securityTypeFromProto(in.GetType()),
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListHoldingsResponse{
		Holdings:    make([]*tradingpb.Holding, 0, len(rows)),
		TotalProfit: totalProfit,
	}
	for _, r := range rows {
		out.Holdings = append(out.Holdings, holdingDecoratedToProto(r))
	}
	return out, nil
}

func (s *Server) SetPublicCount(ctx context.Context, in *tradingpb.SetPublicCountRequest) (*tradingpb.Holding, error) {
	h, err := s.Svc.SetPublicCount(ctx, in.GetId(), in.GetPublicCount())
	if err != nil {
		return nil, err
	}
	return holdingToProto(h, nil), nil
}

func holdingDecoratedToProto(d *service.HoldingDecorated) *tradingpb.Holding {
	if d == nil || d.Holding == nil {
		return nil
	}
	out := holdingToProto(d.Holding, d.Security)
	out.CurrentPrice = d.CurrentPrice
	out.MarketValue = d.MarketValue
	out.Profit = d.Profit
	return out
}

func holdingToProto(h *domain.Holding, sec *domain.Security) *tradingpb.Holding {
	if h == nil {
		return nil
	}
	out := &tradingpb.Holding{
		Id:               h.ID,
		UserId:           h.UserID,
		UserKind:         userKindToProto(h.UserKind),
		AccountId:        h.AccountID,
		Quantity:         h.Quantity,
		WeightedAvgPrice: h.WeightedAvgPrice,
		PublicCount:      h.PublicCount,
		AcquiredAt:       timestamppb.New(h.AcquiredAt),
		UpdatedAt:        timestamppb.New(h.UpdatedAt),
	}
	if sec != nil {
		out.Security = securityToProto(sec)
	}
	return out
}
