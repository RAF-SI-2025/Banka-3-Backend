package server

import (
	"context"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
)

func (s *Server) SettleDividend(ctx context.Context, in *bankpb.SettleDividendRequest) (*bankpb.SettleDividendResponse, error) {
	r, err := s.Svc.SettleDividend(ctx, service.SettleDividendInput{
		AccountID: in.GetAccountId(),
		Amount:    in.GetAmount(),
		Currency:  currencyFromProto(in.GetCurrency()),
		OpID:      in.GetOpId(),
		Purpose:   in.GetPurpose(),
	})
	if err != nil {
		return nil, err
	}
	out := &bankpb.SettleDividendResponse{OpId: r.OpID}
	for _, t := range r.Transactions {
		out.Transactions = append(out.Transactions, transactionToProto(t))
	}
	return out, nil
}
