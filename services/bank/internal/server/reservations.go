package server

import (
	"context"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
)

func (s *Server) ReserveFunds(ctx context.Context, in *bankpb.ReserveFundsRequest) (*bankpb.ReserveFundsResponse, error) {
	res, err := s.Svc.ReserveFunds(ctx, service.ReserveFundsInput{
		AccountID: in.GetAccountId(),
		Amount:    in.GetAmount(),
		Currency:  currencyFromProto(in.GetCurrency()),
		OpID:      in.GetOpId(),
		OpKind:    in.GetOpKind(),
	})
	if err != nil {
		return nil, err
	}
	return &bankpb.ReserveFundsResponse{
		ReservationId: res.ReservationID,
		OpId:          res.OpID,
	}, nil
}

func (s *Server) ReleaseFunds(ctx context.Context, in *bankpb.ReleaseFundsRequest) (*bankpb.ReleaseFundsResponse, error) {
	res, err := s.Svc.ReleaseFunds(ctx, in.GetOpId())
	if err != nil {
		return nil, err
	}
	return &bankpb.ReleaseFundsResponse{Released: res.Released}, nil
}

func (s *Server) CommitReservedFunds(ctx context.Context, in *bankpb.CommitReservedFundsRequest) (*bankpb.CommitReservedFundsResponse, error) {
	r, err := s.Svc.CommitReservedFunds(ctx, service.CommitReservedFundsInput{
		OpID:          in.GetOpId(),
		DestAccountID: in.GetDestAccountId(),
		DestAmount:    in.GetDestAmount(),
		DestCurrency:  currencyFromProto(in.GetDestCurrency()),
		IsActuary:     in.GetIsActuary(),
		Purpose:       in.GetPurpose(),
	})
	if err != nil {
		return nil, err
	}
	out := &bankpb.CommitReservedFundsResponse{OpId: r.OpID}
	for _, t := range r.Transactions {
		out.Transactions = append(out.Transactions, transactionToProto(t))
	}
	return out, nil
}

func (s *Server) TransferBetweenClients(ctx context.Context, in *bankpb.TransferBetweenClientsRequest) (*bankpb.TransferBetweenClientsResponse, error) {
	r, err := s.Svc.TransferBetweenClients(ctx, service.TransferBetweenClientsInput{
		FromAccountID: in.GetFromAccountId(),
		ToAccountID:   in.GetToAccountId(),
		Amount:        in.GetAmount(),
		OpID:          in.GetOpId(),
		OpKind:        in.GetOpKind(),
		IsActuary:     in.GetIsActuary(),
		Purpose:       in.GetPurpose(),
	})
	if err != nil {
		return nil, err
	}
	out := &bankpb.TransferBetweenClientsResponse{OpId: r.OpID}
	for _, t := range r.Transactions {
		out.Transactions = append(out.Transactions, transactionToProto(t))
	}
	return out, nil
}
