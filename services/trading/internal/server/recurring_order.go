package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/schedule"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) CreateRecurringOrder(ctx context.Context, in *tradingpb.CreateRecurringOrderRequest) (*tradingpb.RecurringOrder, error) {
	r, err := s.Svc.CreateRecurringOrder(ctx, service.CreateRecurringOrderInput{
		SecurityID: in.GetSecurityId(),
		Mode:       recurringModeFromProto(in.GetMode()),
		AmountRSD:  in.GetAmountRsd(),
		Quantity:   in.GetQuantity(),
		AccountID:  in.GetAccountId(),
		Cadence:    schedule.Cadence(in.GetCadence()),
		StartDate:  in.GetStartDate(),
	})
	if err != nil {
		return nil, err
	}
	return recurringOrderToProto(r), nil
}

func (s *Server) ListRecurringOrders(ctx context.Context, _ *tradingpb.ListRecurringOrdersRequest) (*tradingpb.ListRecurringOrdersResponse, error) {
	rows, err := s.Svc.ListRecurringOrders(ctx)
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListRecurringOrdersResponse{RecurringOrders: make([]*tradingpb.RecurringOrder, 0, len(rows))}
	for _, r := range rows {
		out.RecurringOrders = append(out.RecurringOrders, recurringOrderToProto(r))
	}
	return out, nil
}

func (s *Server) PauseRecurringOrder(ctx context.Context, in *tradingpb.PauseRecurringOrderRequest) (*tradingpb.RecurringOrder, error) {
	r, err := s.Svc.PauseRecurringOrder(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return recurringOrderToProto(r), nil
}

func (s *Server) ResumeRecurringOrder(ctx context.Context, in *tradingpb.ResumeRecurringOrderRequest) (*tradingpb.RecurringOrder, error) {
	r, err := s.Svc.ResumeRecurringOrder(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return recurringOrderToProto(r), nil
}

func (s *Server) CancelRecurringOrder(ctx context.Context, in *tradingpb.CancelRecurringOrderRequest) (*tradingpb.CancelRecurringOrderResponse, error) {
	if err := s.Svc.CancelRecurringOrder(ctx, in.GetId()); err != nil {
		return nil, err
	}
	return &tradingpb.CancelRecurringOrderResponse{}, nil
}

// RunRecurringOrders fires every due recurring order. Internal-only RPC
// driven by the scheduler service.
func (s *Server) RunRecurringOrders(ctx context.Context, _ *tradingpb.RunRecurringOrdersRequest) (*tradingpb.RunRecurringOrdersResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	n, err := s.Svc.RunRecurringOrders(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunRecurringOrdersResponse{Created: int32(n)}, nil
}

func recurringOrderToProto(r *domain.RecurringOrder) *tradingpb.RecurringOrder {
	if r == nil {
		return nil
	}
	out := &tradingpb.RecurringOrder{
		Id:         r.ID,
		UserId:     r.UserID,
		UserKind:   userKindToProto(r.UserKind),
		SecurityId: r.SecurityID,
		Direction:  directionToProto(r.Direction),
		Mode:       recurringModeToProto(r.Mode),
		AmountRsd:  r.AmountRSD,
		Quantity:   r.Quantity,
		AccountId:  r.AccountID,
		Cadence:    r.Cadence,
		NextRun:    timestamppb.New(r.NextRun),
		Active:     r.Active,
		CreatedAt:  timestamppb.New(r.CreatedAt),
		UpdatedAt:  timestamppb.New(r.UpdatedAt),
	}
	return out
}

func recurringModeFromProto(m tradingpb.RecurringMode) domain.RecurringMode {
	switch m {
	case tradingpb.RecurringMode_RECURRING_MODE_BYAMOUNT:
		return domain.RecurringByAmount
	case tradingpb.RecurringMode_RECURRING_MODE_BYQUANTITY:
		return domain.RecurringByQuantity
	default:
		return ""
	}
}

func recurringModeToProto(m domain.RecurringMode) tradingpb.RecurringMode {
	switch m {
	case domain.RecurringByAmount:
		return tradingpb.RecurringMode_RECURRING_MODE_BYAMOUNT
	case domain.RecurringByQuantity:
		return tradingpb.RecurringMode_RECURRING_MODE_BYQUANTITY
	default:
		return tradingpb.RecurringMode_RECURRING_MODE_UNSPECIFIED
	}
}
