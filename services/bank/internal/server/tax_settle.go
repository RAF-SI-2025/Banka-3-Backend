package server

import (
	"context"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
)

func (s *Server) SettleCapitalGainsTax(ctx context.Context, in *bankpb.SettleCapitalGainsTaxRequest) (*bankpb.SettleCapitalGainsTaxResponse, error) {
	r, err := s.Svc.SettleCapitalGainsTax(ctx, service.SettleCapitalGainsTaxInput{
		AccountID: in.GetAccountId(),
		AmountRSD: in.GetAmountRsd(),
		OpID:      in.GetOpId(),
		Purpose:   in.GetPurpose(),
	})
	if err != nil {
		return nil, err
	}
	out := &bankpb.SettleCapitalGainsTaxResponse{OpId: r.OpID}
	for _, t := range r.Transactions {
		out.Transactions = append(out.Transactions, transactionToProto(t))
	}
	return out, nil
}
