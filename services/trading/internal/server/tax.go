package server

import (
	"context"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) ListTaxPositions(ctx context.Context, in *tradingpb.ListTaxPositionsRequest) (*tradingpb.ListTaxPositionsResponse, error) {
	rows, err := s.Svc.ListTaxPositions(ctx, service.ListTaxPositionsInput{
		UserKind:  userKindFromProto(in.GetUserKind()),
		NameQuery: in.GetNameQuery(),
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListTaxPositionsResponse{Positions: make([]*tradingpb.TaxPosition, 0, len(rows))}
	for _, r := range rows {
		out.Positions = append(out.Positions, &tradingpb.TaxPosition{
			UserId:        r.UserID,
			UserKind:      userKindToProto(r.UserKind),
			DisplayName:   r.DisplayName,
			UnpaidTaxRsd:  r.UnpaidTaxRSD,
			PaidTaxYtdRsd: r.PaidTaxYTDRSD,
		})
	}
	return out, nil
}

func (s *Server) ListRealizedPnL(ctx context.Context, in *tradingpb.ListRealizedPnLRequest) (*tradingpb.ListRealizedPnLResponse, error) {
	rows, err := s.Svc.ListRealizedPnL(ctx, service.ListRealizedPnLInput{
		UserID:   in.GetUserId(),
		UserKind: userKindFromProto(in.GetUserKind()),
		From:     timestampToTime(in.GetFrom()),
		To:       timestampToTime(in.GetTo()),
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListRealizedPnLResponse{Rows: make([]*tradingpb.RealizedPnLRow, 0, len(rows))}
	for _, r := range rows {
		row := &tradingpb.RealizedPnLRow{
			Id:           r.ID,
			SaleAt:       timestamppb.New(r.SaleAt),
			SecurityId:   r.SecurityID,
			Ticker:       r.Ticker,
			AccountId:    r.AccountID,
			Quantity:     r.Quantity,
			CostBasisAmt: r.CostBasisAmt,
			ProceedsAmt:  r.ProceedsAmt,
			Currency:     currencyToProto(r.Currency),
			ProfitNative: r.ProfitNative,
			ProfitRsd:    r.ProfitRSD,
			TaxAmountRsd: r.TaxAmountRSD,
			Taxed:        r.Taxed,
			TaxOpId:      r.TaxOpID,
		}
		if r.TaxedAt != nil {
			row.TaxedAt = timestamppb.New(*r.TaxedAt)
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

func timestampToTime(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil || !ts.IsValid() {
		return nil
	}
	t := ts.AsTime()
	return &t
}

func (s *Server) RunTax(ctx context.Context, in *tradingpb.RunTaxRequest) (*tradingpb.RunTaxResponse, error) {
	r, err := s.Svc.RunTax(ctx, service.RunTaxInput{
		UserID:   in.GetUserId(),
		UserKind: userKindFromProto(in.GetUserKind()),
	})
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunTaxResponse{
		UsersTaxed:        r.UsersTaxed,
		TotalCollectedRsd: r.TotalCollectedRSD,
	}, nil
}
