package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

func (s *Server) ListActuaryPerformances(ctx context.Context, in *tradingpb.ListActuaryPerformancesRequest) (*tradingpb.ListActuaryPerformancesResponse, error) {
	rows, err := s.Svc.ListActuaryPerformances(ctx, service.ListActuaryPerformancesInput{
		Type:      domain.ActuaryType(in.GetType()),
		NameQuery: in.GetNameQuery(),
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListActuaryPerformancesResponse{Rows: make([]*tradingpb.ActuaryPerformance, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, &tradingpb.ActuaryPerformance{
			UserId:        r.UserID,
			DisplayName:   r.DisplayName,
			Type:          actuaryTypeToProto(r.Type),
			ProfitRsd:     r.ProfitRSD,
			RealizedCount: r.RealizedCount,
		})
	}
	return out, nil
}

func (s *Server) ReassignSupervisorAssets(ctx context.Context, in *tradingpb.ReassignSupervisorAssetsRequest) (*tradingpb.ReassignSupervisorAssetsResponse, error) {
	n, err := s.Svc.ReassignSupervisorAssets(ctx, in.GetFromUserId(), in.GetToUserId())
	if err != nil {
		return nil, err
	}
	return &tradingpb.ReassignSupervisorAssetsResponse{FundsReassigned: int32(n)}, nil
}

func (s *Server) ListBankFundPositions(ctx context.Context, in *tradingpb.ListBankFundPositionsRequest) (*tradingpb.ListBankFundPositionsResponse, error) {
	rows, err := s.Svc.ListBankFundPositions(ctx)
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListBankFundPositionsResponse{Rows: make([]*tradingpb.BankFundPosition, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, &tradingpb.BankFundPosition{
			Position:           fundPositionToProto(r.Position.Position, r.Position.FundName, r.Position.SharePct, r.Position.CurrentValueRSD, r.Position.ProfitRSD),
			FundName:           r.Position.FundName,
			ManagerUserId:      r.ManagerUserID,
			ManagerDisplayName: r.ManagerDisplayName,
		})
	}
	return out, nil
}
