package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) GetActuaryInfo(ctx context.Context, in *tradingpb.GetActuaryInfoRequest) (*tradingpb.ActuaryInfo, error) {
	a, err := s.Svc.GetActuaryInfo(ctx, in.GetEmployeeId())
	if err != nil {
		return nil, err
	}
	return actuaryToProto(a), nil
}

func (s *Server) ListActuaries(ctx context.Context, in *tradingpb.ListActuariesRequest) (*tradingpb.ListActuariesResponse, error) {
	t := actuaryTypeFromProto(in.GetType())
	as, total, err := s.Svc.ListActuaries(ctx, t, int(in.GetPage()), int(in.GetPageSize()))
	if err != nil {
		return nil, err
	}
	out := make([]*tradingpb.ActuaryInfo, 0, len(as))
	for _, a := range as {
		out = append(out, actuaryToProto(a))
	}
	page := int(in.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(in.GetPageSize())
	if pageSize < 1 {
		pageSize = 50
	}
	return &tradingpb.ListActuariesResponse{
		Actuaries: out,
		Page:      int32(page),
		PageSize:  int32(pageSize),
		Total:     total,
	}, nil
}

func (s *Server) UpsertActuaryInfo(ctx context.Context, in *tradingpb.UpsertActuaryInfoRequest) (*tradingpb.ActuaryInfo, error) {
	a, err := s.Svc.UpsertActuaryInfo(ctx, service.UpsertActuaryInfoInput{
		EmployeeID:   in.GetEmployeeId(),
		Type:         actuaryTypeFromProto(in.GetType()),
		DailyLimit:   in.GetDailyLimit(),
		NeedApproval: in.GetNeedApproval(),
	})
	if err != nil {
		return nil, err
	}
	return actuaryToProto(a), nil
}

func (s *Server) UpdateActuaryLimit(ctx context.Context, in *tradingpb.UpdateActuaryLimitRequest) (*tradingpb.ActuaryInfo, error) {
	a, err := s.Svc.UpdateActuaryLimit(ctx, in.GetEmployeeId(), in.GetDailyLimit())
	if err != nil {
		return nil, err
	}
	return actuaryToProto(a), nil
}

func (s *Server) ResetActuaryUsedLimit(ctx context.Context, in *tradingpb.ResetActuaryUsedLimitRequest) (*tradingpb.ActuaryInfo, error) {
	a, err := s.Svc.ResetActuaryUsedLimit(ctx, in.GetEmployeeId())
	if err != nil {
		return nil, err
	}
	return actuaryToProto(a), nil
}

func (s *Server) SetActuaryNeedApproval(ctx context.Context, in *tradingpb.SetActuaryNeedApprovalRequest) (*tradingpb.ActuaryInfo, error) {
	a, err := s.Svc.SetActuaryNeedApproval(ctx, in.GetEmployeeId(), in.GetNeedApproval())
	if err != nil {
		return nil, err
	}
	return actuaryToProto(a), nil
}

func (s *Server) RunDailyResetActuaries(ctx context.Context, _ *emptypb.Empty) (*tradingpb.RunDailyResetActuariesResponse, error) {
	n, err := s.Svc.RunDailyResetActuaries(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunDailyResetActuariesResponse{Affected: int32(n)}, nil
}

// =====================================================================
// Conversions
// =====================================================================

func actuaryToProto(a *domain.ActuaryInfo) *tradingpb.ActuaryInfo {
	return &tradingpb.ActuaryInfo{
		EmployeeId:   a.EmployeeID,
		Type:         actuaryTypeToProto(a.Type),
		DailyLimit:   a.DailyLimit,
		UsedLimit:    a.UsedLimit,
		NeedApproval: a.NeedApproval,
		CreatedAt:    timestamppb.New(a.CreatedAt),
		UpdatedAt:    timestamppb.New(a.UpdatedAt),
	}
}

func actuaryTypeToProto(t domain.ActuaryType) tradingpb.ActuaryType {
	switch t {
	case domain.ActuarySupervisor:
		return tradingpb.ActuaryType_ACTUARY_TYPE_SUPERVISOR
	case domain.ActuaryAgent:
		return tradingpb.ActuaryType_ACTUARY_TYPE_AGENT
	}
	return tradingpb.ActuaryType_ACTUARY_TYPE_UNSPECIFIED
}

func actuaryTypeFromProto(t tradingpb.ActuaryType) domain.ActuaryType {
	switch t {
	case tradingpb.ActuaryType_ACTUARY_TYPE_SUPERVISOR:
		return domain.ActuarySupervisor
	case tradingpb.ActuaryType_ACTUARY_TYPE_AGENT:
		return domain.ActuaryAgent
	}
	return ""
}
