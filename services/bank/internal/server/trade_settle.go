package server

import (
	"context"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
)

func (s *Server) SettleTrade(ctx context.Context, in *bankpb.SettleTradeRequest) (*bankpb.SettleTradeResponse, error) {
	r, err := s.Svc.SettleTrade(ctx, service.SettleTradeInput{
		AccountID: in.GetAccountId(),
		Direction: in.GetDirection(),
		Currency:  currencyFromProto(in.GetCurrency()),
		Amount:    in.GetAmount(),
		OpID:      in.GetOpId(),
		IsActuary: in.GetIsActuary(),
		Purpose:   in.GetPurpose(),
	})
	if err != nil {
		return nil, err
	}
	out := &bankpb.SettleTradeResponse{OpId: r.OpID}
	for _, t := range r.Transactions {
		out.Transactions = append(out.Transactions, transactionToProto(t))
	}
	return out, nil
}

func (s *Server) SettleForexFill(ctx context.Context, in *bankpb.SettleForexFillRequest) (*bankpb.SettleForexFillResponse, error) {
	r, err := s.Svc.SettleForexFill(ctx, service.SettleForexFillInput{
		Direction:     in.GetDirection(),
		BaseCurrency:  currencyFromProto(in.GetBaseCurrency()),
		BaseAmount:    in.GetBaseAmount(),
		QuoteCurrency: currencyFromProto(in.GetQuoteCurrency()),
		QuoteAmount:   in.GetQuoteAmount(),
		OpID:          in.GetOpId(),
		Purpose:       in.GetPurpose(),
	})
	if err != nil {
		return nil, err
	}
	out := &bankpb.SettleForexFillResponse{OpId: r.OpID}
	for _, t := range r.Transactions {
		out.Transactions = append(out.Transactions, transactionToProto(t))
	}
	return out, nil
}
