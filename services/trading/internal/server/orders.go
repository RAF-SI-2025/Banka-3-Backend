package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) CreateOrder(ctx context.Context, in *tradingpb.CreateOrderRequest) (*tradingpb.Order, error) {
	out, err := s.Svc.CreateOrder(ctx, service.CreateOrderInput{
		SecurityID: in.GetSecurityId(),
		OrderType:  orderTypeFromProto(in.GetOrderType()),
		Direction:  directionFromProto(in.GetDirection()),
		Quantity:   in.GetQuantity(),
		LimitPrice: in.GetLimitPrice(),
		StopPrice:  in.GetStopPrice(),
		AllOrNone:  in.GetAllOrNone(),
		Margin:     in.GetMargin(),
		AccountID:  in.GetAccountId(),
	})
	if err != nil {
		return nil, err
	}
	return orderToProto(out), nil
}

func (s *Server) GetOrder(ctx context.Context, in *tradingpb.GetOrderRequest) (*tradingpb.Order, error) {
	o, err := s.Svc.GetOrder(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return orderToProto(o), nil
}

func (s *Server) ListOrders(ctx context.Context, in *tradingpb.ListOrdersRequest) (*tradingpb.ListOrdersResponse, error) {
	rows, total, err := s.Svc.ListOrders(ctx, service.ListOrdersInput{
		Status:     in.GetStatus(),
		UserKind:   userKindFromProto(in.GetUserKind()),
		UserID:     in.GetUserId(),
		SecurityID: in.GetSecurityId(),
		Page:       int(in.GetPage()),
		PageSize:   int(in.GetPageSize()),
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListOrdersResponse{Orders: make([]*tradingpb.Order, 0, len(rows))}
	for _, o := range rows {
		out.Orders = append(out.Orders, orderToProto(o))
	}
	page := int(in.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(in.GetPageSize())
	if pageSize < 1 {
		pageSize = 50
	}
	out.Page = int32(page)
	out.PageSize = int32(pageSize)
	out.Total = total
	return out, nil
}

func (s *Server) ApproveOrder(ctx context.Context, in *tradingpb.ApproveOrderRequest) (*tradingpb.Order, error) {
	o, err := s.Svc.ApproveOrder(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return orderToProto(o), nil
}

func (s *Server) DeclineOrder(ctx context.Context, in *tradingpb.DeclineOrderRequest) (*tradingpb.Order, error) {
	o, err := s.Svc.DeclineOrder(ctx, in.GetId(), in.GetReason())
	if err != nil {
		return nil, err
	}
	return orderToProto(o), nil
}

func (s *Server) CancelOrder(ctx context.Context, in *tradingpb.CancelOrderRequest) (*tradingpb.Order, error) {
	o, err := s.Svc.CancelOrder(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return orderToProto(o), nil
}

func orderToProto(o *domain.Order) *tradingpb.Order {
	if o == nil {
		return nil
	}
	out := &tradingpb.Order{
		Id:                o.ID,
		UserId:            o.UserID,
		UserKind:          userKindToProto(o.UserKind),
		SecurityId:        o.SecurityID,
		OrderType:         orderTypeToProto(o.OrderType),
		Direction:         directionToProto(o.Direction),
		Quantity:          o.Quantity,
		ContractSize:      o.ContractSize,
		PricePerUnit:      o.PricePerUnit,
		LimitPrice:        o.LimitPrice,
		StopPrice:         o.StopPrice,
		AllOrNone:         o.AllOrNone,
		Margin:            o.Margin,
		AccountId:         o.AccountID,
		Status:            orderStatusToProto(o.Status),
		ApprovedBy:        o.ApprovedBy,
		ApprovalRequired:  o.ApprovalRequired,
		IsDone:            o.IsDone,
		Cancelled:         o.Cancelled,
		Triggered:         o.Triggered,
		AfterHours:        o.AfterHours,
		RemainingQuantity: o.RemainingQuantity,
		LastModification:  timestamppb.New(o.LastModification),
		CreatedAt:         timestamppb.New(o.CreatedAt),
	}
	if o.ApprovedAt != nil {
		out.ApprovedAt = timestamppb.New(*o.ApprovedAt)
	}
	return out
}

func orderTypeToProto(t domain.OrderType) tradingpb.OrderType {
	switch t {
	case domain.OrderMarket:
		return tradingpb.OrderType_ORDER_TYPE_MARKET
	case domain.OrderLimit:
		return tradingpb.OrderType_ORDER_TYPE_LIMIT
	case domain.OrderStop:
		return tradingpb.OrderType_ORDER_TYPE_STOP
	case domain.OrderStopLimit:
		return tradingpb.OrderType_ORDER_TYPE_STOP_LIMIT
	}
	return tradingpb.OrderType_ORDER_TYPE_UNSPECIFIED
}

func orderTypeFromProto(t tradingpb.OrderType) domain.OrderType {
	switch t {
	case tradingpb.OrderType_ORDER_TYPE_MARKET:
		return domain.OrderMarket
	case tradingpb.OrderType_ORDER_TYPE_LIMIT:
		return domain.OrderLimit
	case tradingpb.OrderType_ORDER_TYPE_STOP:
		return domain.OrderStop
	case tradingpb.OrderType_ORDER_TYPE_STOP_LIMIT:
		return domain.OrderStopLimit
	}
	return ""
}

func directionToProto(d domain.Direction) tradingpb.Direction {
	switch d {
	case domain.DirectionBuy:
		return tradingpb.Direction_DIRECTION_BUY
	case domain.DirectionSell:
		return tradingpb.Direction_DIRECTION_SELL
	}
	return tradingpb.Direction_DIRECTION_UNSPECIFIED
}

func directionFromProto(d tradingpb.Direction) domain.Direction {
	switch d {
	case tradingpb.Direction_DIRECTION_BUY:
		return domain.DirectionBuy
	case tradingpb.Direction_DIRECTION_SELL:
		return domain.DirectionSell
	}
	return ""
}

func orderStatusToProto(st domain.OrderStatus) tradingpb.OrderStatus {
	switch st {
	case domain.OrderStatusPending:
		return tradingpb.OrderStatus_ORDER_STATUS_PENDING
	case domain.OrderStatusApproved:
		return tradingpb.OrderStatus_ORDER_STATUS_APPROVED
	case domain.OrderStatusDeclined:
		return tradingpb.OrderStatus_ORDER_STATUS_DECLINED
	}
	return tradingpb.OrderStatus_ORDER_STATUS_UNSPECIFIED
}

func userKindToProto(k domain.UserKind) tradingpb.UserKind {
	switch k {
	case domain.KindClient:
		return tradingpb.UserKind_USER_KIND_CLIENT
	case domain.KindEmployee:
		return tradingpb.UserKind_USER_KIND_EMPLOYEE
	}
	return tradingpb.UserKind_USER_KIND_UNSPECIFIED
}

func userKindFromProto(k tradingpb.UserKind) domain.UserKind {
	switch k {
	case tradingpb.UserKind_USER_KIND_CLIENT:
		return domain.KindClient
	case tradingpb.UserKind_USER_KIND_EMPLOYEE:
		return domain.KindEmployee
	}
	return ""
}
