package trading

import (
	"context"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/trading"
)

func externalOTCThreadToProto(rec ExternalOTCThreadRecord) *tradingpb.ExternalOTCThread {
	return &tradingpb.ExternalOTCThread{
		Id:                 rec.ID,
		Direction:          rec.Direction,
		RemoteBankCode:     rec.RemoteBankCode,
		RemoteThreadId:     rec.RemoteThreadID,
		RemoteUserRef:      rec.RemoteUserRef,
		RemoteDisplayName:  rec.RemoteDisplayName,
		RemoteAccountRef:   rec.RemoteAccountRef,
		LocalUserId:        rec.LocalUserID,
		LocalUserKind:      rec.LocalUserKind,
		LocalAccountId:     rec.LocalAccountID,
		LocalAccountNumber: rec.LocalAccountNumber,
		LocalRole:          rec.LocalRole,
		SecurityId:         rec.SecurityID,
		SecurityTicker:     rec.SecurityTicker,
		SellerHoldingId:    rec.SellerHoldingID,
		Quantity:           rec.Quantity,
		PricePerUnit:       rec.PricePerUnit,
		Premium:            rec.Premium,
		Currency:           rec.Currency,
		SettlementDate:     rec.SettlementDate.Format("2006-01-02"),
		ModifiedBySide:     rec.ModifiedBySide,
		Status:             rec.Status,
		CreatedAt:          rec.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:          rec.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func externalOTCIterationToProto(rec ExternalOTCIterationRecord) *tradingpb.ExternalOTCIteration {
	return &tradingpb.ExternalOTCIteration{
		Id:             rec.ID,
		ThreadId:       rec.ThreadID,
		ProposedBySide: rec.ProposedBySide,
		Quantity:       rec.Quantity,
		PricePerUnit:   rec.PricePerUnit,
		Premium:        rec.Premium,
		SettlementDate: rec.SettlementDate.Format("2006-01-02"),
		CreatedAt:      rec.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func externalOTCContractToProto(rec ExternalOTCContractRecord) *tradingpb.ExternalOTCContract {
	out := &tradingpb.ExternalOTCContract{
		Id:                 rec.ID,
		ThreadId:           rec.ThreadID,
		Direction:          rec.Direction,
		RemoteBankCode:     rec.RemoteBankCode,
		RemoteThreadId:     rec.RemoteThreadID,
		RemoteUserRef:      rec.RemoteUserRef,
		RemoteDisplayName:  rec.RemoteDisplayName,
		RemoteAccountRef:   rec.RemoteAccountRef,
		LocalUserId:        rec.LocalUserID,
		LocalUserKind:      rec.LocalUserKind,
		LocalAccountId:     rec.LocalAccountID,
		LocalAccountNumber: rec.LocalAccountNumber,
		LocalRole:          rec.LocalRole,
		SecurityId:         rec.SecurityID,
		SecurityTicker:     rec.SecurityTicker,
		SellerHoldingId:    rec.SellerHoldingID,
		Quantity:           rec.Quantity,
		StrikePrice:        rec.StrikePrice,
		PremiumPaid:        rec.PremiumPaid,
		Currency:           rec.Currency,
		SettlementDate:     rec.SettlementDate.Format("2006-01-02"),
		AcceptedBySide:     rec.AcceptedBySide,
		Status:             rec.Status,
		PremiumOpId:        rec.PremiumOpID,
		ExerciseOpId:       rec.ExerciseOpID,
		CreatedAt:          rec.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:          rec.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if rec.ExercisedAt != nil {
		out.ExercisedAt = rec.ExercisedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (s *Server) ListExternalOTCThreads(ctx context.Context, req *tradingpb.ListExternalOTCThreadsRequest) (*tradingpb.ListExternalOTCThreadsResponse, error) {
	rows, err := s.ListExternalOTCThreadsForCaller(ctx, req.GetStatus())
	if err != nil {
		return nil, err
	}
	out := make([]*tradingpb.ExternalOTCThread, 0, len(rows))
	for _, row := range rows {
		out = append(out, externalOTCThreadToProto(row))
	}
	return &tradingpb.ListExternalOTCThreadsResponse{Threads: out}, nil
}

func (s *Server) GetExternalOTCThread(ctx context.Context, req *tradingpb.GetExternalOTCThreadRequest) (*tradingpb.GetExternalOTCThreadResponse, error) {
	thread, iterations, contract, err := s.GetExternalOTCThreadForCaller(ctx, req.GetThreadId())
	if err != nil {
		return nil, err
	}
	outIterations := make([]*tradingpb.ExternalOTCIteration, 0, len(iterations))
	for _, row := range iterations {
		outIterations = append(outIterations, externalOTCIterationToProto(row))
	}
	resp := &tradingpb.GetExternalOTCThreadResponse{
		Thread:     externalOTCThreadToProto(*thread),
		Iterations: outIterations,
	}
	if contract != nil {
		resp.Contract = externalOTCContractToProto(*contract)
	}
	return resp, nil
}

func (s *Server) CreateExternalOTCOffer(ctx context.Context, req *tradingpb.CreateExternalOTCOfferRequest) (*tradingpb.CreateExternalOTCOfferResponse, error) {
	thread, err := s.CreateExternalOTCOfferForCaller(ctx, CreateExternalOTCOfferInput{
		RemoteBankCode:    req.GetRemoteBankCode(),
		RemoteThreadID:    req.GetRemoteThreadId(),
		RemoteUserRef:     req.GetRemoteUserRef(),
		RemoteDisplayName: req.GetRemoteDisplayName(),
		BuyerAccountID:    req.GetBuyerAccountId(),
		RemoteAccountRef:  "",
		SellerHoldingID:   req.GetSellerHoldingId(),
		SecurityTicker:    req.GetSecurityTicker(),
		SecurityType:      req.GetSecurityType(),
		Currency:          req.GetCurrency(),
		Quantity:          req.GetQuantity(),
		PricePerUnit:      req.GetPricePerUnit(),
		Premium:           req.GetPremium(),
		SettlementDate:    req.GetSettlementDate(),
	})
	if err != nil {
		return nil, err
	}
	return &tradingpb.CreateExternalOTCOfferResponse{LocalMirror: externalOTCThreadToProto(*thread)}, nil
}

func (s *Server) CounterExternalOTCOffer(ctx context.Context, req *tradingpb.ExternalOTCActionRequest) (*tradingpb.ExternalOTCActionResponse, error) {
	thread, err := s.CounterExternalOTCThreadForCaller(ctx, CounterExternalOTCThreadInput{
		ThreadID:       req.GetThreadId(),
		Quantity:       req.GetQuantity(),
		PricePerUnit:   req.GetPricePerUnit(),
		Premium:        req.GetPremium(),
		SettlementDate: req.GetSettlementDate(),
	})
	if err != nil {
		return nil, err
	}
	return &tradingpb.ExternalOTCActionResponse{Thread: externalOTCThreadToProto(*thread)}, nil
}

func (s *Server) WithdrawExternalOTCOffer(ctx context.Context, req *tradingpb.ExternalOTCActionRequest) (*tradingpb.ExternalOTCActionResponse, error) {
	thread, err := s.WithdrawExternalOTCThreadForCaller(ctx, req.GetThreadId())
	if err != nil {
		return nil, err
	}
	return &tradingpb.ExternalOTCActionResponse{Thread: externalOTCThreadToProto(*thread)}, nil
}

func (s *Server) AcceptExternalOTCOffer(ctx context.Context, req *tradingpb.ExternalOTCActionRequest) (*tradingpb.ExternalOTCActionResponse, error) {
	thread, _, err := s.AcceptExternalOTCThreadForCaller(ctx, req.GetThreadId())
	if err != nil {
		return nil, err
	}
	return &tradingpb.ExternalOTCActionResponse{Thread: externalOTCThreadToProto(*thread)}, nil
}

func (s *Server) ListExternalOTCContracts(ctx context.Context, req *tradingpb.ListExternalOTCContractsRequest) (*tradingpb.ListExternalOTCContractsResponse, error) {
	rows, err := s.ListExternalOTCContractsForCaller(ctx, req.GetStatus())
	if err != nil {
		return nil, err
	}
	out := make([]*tradingpb.ExternalOTCContract, 0, len(rows))
	for _, row := range rows {
		out = append(out, externalOTCContractToProto(row))
	}
	return &tradingpb.ListExternalOTCContractsResponse{Contracts: out}, nil
}

func (s *Server) ExerciseExternalOTCContract(ctx context.Context, req *tradingpb.ExerciseExternalOTCContractRequest) (*tradingpb.ExerciseExternalOTCContractResponse, error) {
	contract, err := s.MarkExternalOTCContractExercisedForCaller(ctx, req.GetContractId(), req.GetExerciseOpId(), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return &tradingpb.ExerciseExternalOTCContractResponse{Contract: externalOTCContractToProto(*contract)}, nil
}
