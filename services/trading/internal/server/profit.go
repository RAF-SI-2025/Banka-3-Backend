package server

import (
	"context"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func (s *Server) GetBankProfitTimeseries(ctx context.Context, in *tradingpb.GetBankProfitTimeseriesRequest) (*tradingpb.GetBankProfitTimeseriesResponse, error) {
	var from, to time.Time
	if f := in.GetFrom(); f != nil {
		from = f.AsTime()
	}
	if t := in.GetTo(); t != nil {
		to = t.AsTime()
	}
	res, err := s.Svc.GetBankProfitTimeseries(ctx, service.GetBankProfitTimeseriesInput{
		Bucket: in.GetBucket(),
		From:   from,
		To:     to,
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.GetBankProfitTimeseriesResponse{
		Buckets:  make([]*tradingpb.BankProfitBucket, 0, len(res.Buckets)),
		TotalRsd: res.TotalRSD,
	}
	for _, b := range res.Buckets {
		out.Buckets = append(out.Buckets, &tradingpb.BankProfitBucket{
			PeriodStart:   timestamppb.New(b.PeriodStart),
			ProfitRsd:     b.ProfitRSD,
			TradingRsd:    b.TradingRSD,
			FundRsd:       b.FundRSD,
			CumulativeRsd: b.CumulativeRSD,
			RealizedCount: b.RealizedCount,
		})
	}
	return out, nil
}

func (s *Server) ListBankFundPositions(ctx context.Context, in *tradingpb.ListBankFundPositionsRequest) (*tradingpb.ListBankFundPositionsResponse, error) {
	rows, err := s.Svc.ListBankFundPositions(ctx)
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListBankFundPositionsResponse{Rows: make([]*tradingpb.BankFundPosition, 0, len(rows))}
	for _, r := range rows {
		desc := ""
		if r.Position.Fund != nil {
			desc = r.Position.Fund.Description
		}
		out.Rows = append(out.Rows, &tradingpb.BankFundPosition{
			Position:           fundPositionToProto(r.Position.Position, r.Position.FundName, r.Position.SharePct, r.Position.CurrentValueRSD, r.Position.ProfitRSD, desc, r.Position.FundTotalValueRSD),
			FundName:           r.Position.FundName,
			ManagerUserId:      r.ManagerUserID,
			ManagerDisplayName: r.ManagerDisplayName,
		})
	}
	return out, nil
}
