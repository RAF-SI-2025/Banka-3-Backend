package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

func (s *Server) ListTaxPositions(ctx context.Context, in *tradingpb.ListTaxPositionsRequest) (*tradingpb.ListTaxPositionsResponse, error) {
	rows, err := s.Svc.ListTaxPositions(ctx, service.ListTaxPositionsInput{
		UserKind: userKindFromProto(in.GetUserKind()),
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListTaxPositionsResponse{Positions: make([]*tradingpb.TaxPosition, 0, len(rows))}
	for _, r := range rows {
		out.Positions = append(out.Positions, &tradingpb.TaxPosition{
			UserId:         r.UserID,
			UserKind:       userKindToProto(r.UserKind),
			DisplayName:    "",
			UnpaidTaxRsd:   r.UnpaidTaxRSD,
			PaidTaxYtdRsd:  r.PaidTaxYTDRSD,
		})
	}
	return out, nil
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
