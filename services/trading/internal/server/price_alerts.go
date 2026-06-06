package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) CreatePriceAlert(ctx context.Context, in *tradingpb.CreatePriceAlertRequest) (*tradingpb.PriceAlert, error) {
	a, err := s.Svc.CreatePriceAlert(ctx, service.CreatePriceAlertInput{
		SecurityID: in.GetSecurityId(),
		Threshold:  in.GetThreshold(),
		Condition:  priceAlertConditionFromProto(in.GetCondition()),
	})
	if err != nil {
		return nil, err
	}
	return priceAlertToProto(a), nil
}

func (s *Server) ListPriceAlerts(ctx context.Context, _ *tradingpb.ListPriceAlertsRequest) (*tradingpb.ListPriceAlertsResponse, error) {
	rows, err := s.Svc.ListPriceAlerts(ctx)
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListPriceAlertsResponse{Alerts: make([]*tradingpb.PriceAlert, 0, len(rows))}
	for _, a := range rows {
		out.Alerts = append(out.Alerts, priceAlertToProto(a))
	}
	return out, nil
}

func (s *Server) DeletePriceAlert(ctx context.Context, in *tradingpb.DeletePriceAlertRequest) (*tradingpb.DeletePriceAlertResponse, error) {
	if err := s.Svc.DeletePriceAlert(ctx, in.GetId()); err != nil {
		return nil, err
	}
	return &tradingpb.DeletePriceAlertResponse{}, nil
}

// RunPriceAlertSweep checks every active alert against its security's
// current price. Internal-only RPC driven by the scheduler.
func (s *Server) RunPriceAlertSweep(ctx context.Context, _ *tradingpb.RunPriceAlertSweepRequest) (*tradingpb.RunPriceAlertSweepResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	n, err := s.Svc.RunPriceAlertSweep(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunPriceAlertSweepResponse{Triggered: int32(n)}, nil
}

func priceAlertToProto(a *domain.PriceAlert) *tradingpb.PriceAlert {
	if a == nil {
		return nil
	}
	out := &tradingpb.PriceAlert{
		Id:         a.ID,
		UserId:     a.UserID,
		UserKind:   userKindToProto(a.UserKind),
		SecurityId: a.SecurityID,
		Threshold:  a.Threshold,
		Condition:  priceAlertConditionToProto(a.Condition),
		IsActive:   a.IsActive,
		CreatedAt:  timestamppb.New(a.CreatedAt),
	}
	if a.TriggeredAt != nil {
		out.TriggeredAt = timestamppb.New(*a.TriggeredAt)
	}
	return out
}

func priceAlertConditionFromProto(c tradingpb.PriceAlertCondition) domain.PriceAlertCondition {
	switch c {
	case tradingpb.PriceAlertCondition_PRICE_ALERT_CONDITION_ABOVE:
		return domain.PriceAlertAbove
	case tradingpb.PriceAlertCondition_PRICE_ALERT_CONDITION_BELOW:
		return domain.PriceAlertBelow
	default:
		return ""
	}
}

func priceAlertConditionToProto(c domain.PriceAlertCondition) tradingpb.PriceAlertCondition {
	switch c {
	case domain.PriceAlertAbove:
		return tradingpb.PriceAlertCondition_PRICE_ALERT_CONDITION_ABOVE
	case domain.PriceAlertBelow:
		return tradingpb.PriceAlertCondition_PRICE_ALERT_CONDITION_BELOW
	default:
		return tradingpb.PriceAlertCondition_PRICE_ALERT_CONDITION_UNSPECIFIED
	}
}
