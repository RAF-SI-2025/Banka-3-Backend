// OTC gRPC handlers (c4-PR2). Translate the proto surface to the
// service layer and back. Pattern mirrors server/portfolio.go.

package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) ListPublicHoldings(ctx context.Context, in *tradingpb.ListPublicHoldingsRequest) (*tradingpb.ListPublicHoldingsResponse, error) {
	rows, err := s.Svc.ListPublicHoldings(ctx, in.GetTicker())
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListPublicHoldingsResponse{
		Items: make([]*tradingpb.PublicHoldingItem, 0, len(rows)),
	}
	for _, r := range rows {
		item := &tradingpb.PublicHoldingItem{
			HoldingId:         r.Holding.ID,
			SellerId:          r.Holding.UserID,
			SellerKind:        userKindToProto(r.Holding.UserKind),
			SellerAccountId:   r.Holding.AccountID,
			SellerDisplayName: r.SellerDisplayName,
			Security:          securityToProto(r.Security),
			AvailableCount:    r.AvailableCount,
			PublicCount:       r.Holding.PublicCount,
			ReservedCount:     r.Holding.ReservedCount,
			CurrentPrice:      r.CurrentPrice,
			Currency:          currencyToProto(r.Security.Currency),
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *Server) CreateOTCOffer(ctx context.Context, in *tradingpb.CreateOTCOfferRequest) (*tradingpb.OTCOffer, error) {
	offer, err := s.Svc.CreateOTCOffer(ctx, service.CreateOTCOfferInput{
		SellerHoldingID: in.GetSellerHoldingId(),
		BuyerAccountID:  in.GetBuyerAccountId(),
		SellerAccountID: in.GetSellerAccountId(),
		Quantity:        in.GetQuantity(),
		PricePerUnit:    in.GetPricePerUnit(),
		Premium:         in.GetPremium(),
		SettlementDate:  in.GetSettlementDate().AsTime(),
	})
	if err != nil {
		return nil, err
	}
	return otcOfferToProto(s, ctx, offer), nil
}

func (s *Server) CounterOfferOTC(ctx context.Context, in *tradingpb.CounterOfferOTCRequest) (*tradingpb.OTCOffer, error) {
	offer, err := s.Svc.CounterOfferOTC(ctx, service.CounterOfferOTCInput{
		ThreadID:       in.GetThreadId(),
		Quantity:       in.GetQuantity(),
		PricePerUnit:   in.GetPricePerUnit(),
		Premium:        in.GetPremium(),
		SettlementDate: in.GetSettlementDate().AsTime(),
	})
	if err != nil {
		return nil, err
	}
	return otcOfferToProto(s, ctx, offer), nil
}

func (s *Server) WithdrawOTCOffer(ctx context.Context, in *tradingpb.WithdrawOTCOfferRequest) (*tradingpb.OTCOffer, error) {
	offer, err := s.Svc.WithdrawOTCOffer(ctx, in.GetThreadId())
	if err != nil {
		return nil, err
	}
	return otcOfferToProto(s, ctx, offer), nil
}

func (s *Server) ListOTCThreads(ctx context.Context, in *tradingpb.ListOTCThreadsRequest) (*tradingpb.ListOTCThreadsResponse, error) {
	rows, err := s.Svc.ListOTCThreads(ctx, service.ListOTCThreadsInput{
		PartyUserID:   in.GetPartyUserId(),
		PartyUserKind: userKindFromProto(in.GetPartyUserKind()),
		Status:        in.GetStatus(),
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListOTCThreadsResponse{Threads: make([]*tradingpb.OTCOffer, 0, len(rows))}
	for _, o := range rows {
		out.Threads = append(out.Threads, otcOfferToProto(s, ctx, o))
	}
	return out, nil
}

func (s *Server) GetOTCThread(ctx context.Context, in *tradingpb.GetOTCThreadRequest) (*tradingpb.GetOTCThreadResponse, error) {
	res, err := s.Svc.GetOTCThread(ctx, in.GetThreadId())
	if err != nil {
		return nil, err
	}
	out := &tradingpb.GetOTCThreadResponse{
		Iterations: make([]*tradingpb.OTCOffer, 0, len(res.Iterations)),
	}
	for _, o := range res.Iterations {
		out.Iterations = append(out.Iterations, otcOfferToProto(s, ctx, o))
	}
	if res.Contract != nil {
		out.Contract = otcContractToProto(s, ctx, res.Contract)
	}
	return out, nil
}

func (s *Server) AcceptOTCOffer(ctx context.Context, in *tradingpb.AcceptOTCOfferRequest) (*tradingpb.AcceptOTCOfferResponse, error) {
	res, err := s.Svc.AcceptOTCOffer(ctx, service.AcceptOTCOfferInput{ThreadID: in.GetThreadId()})
	if err != nil {
		return nil, err
	}
	return &tradingpb.AcceptOTCOfferResponse{
		Contract:     otcContractToProto(s, ctx, res.Contract),
		PremiumOpId:  res.PremiumOpID,
	}, nil
}

func (s *Server) ListOTCContracts(ctx context.Context, in *tradingpb.ListOTCContractsRequest) (*tradingpb.ListOTCContractsResponse, error) {
	rows, err := s.Svc.ListOTCContracts(ctx, service.ListOTCContractsInput{
		PartyUserID:   in.GetPartyUserId(),
		PartyUserKind: userKindFromProto(in.GetPartyUserKind()),
		Status:        in.GetStatus(),
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListOTCContractsResponse{Contracts: make([]*tradingpb.OTCContract, 0, len(rows))}
	for _, c := range rows {
		out.Contracts = append(out.Contracts, otcContractToProto(s, ctx, c))
	}
	return out, nil
}

func (s *Server) GetOTCContract(ctx context.Context, in *tradingpb.GetOTCContractRequest) (*tradingpb.OTCContract, error) {
	c, err := s.Svc.GetOTCContract(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return otcContractToProto(s, ctx, c), nil
}

func (s *Server) ExerciseOTCContract(ctx context.Context, in *tradingpb.ExerciseOTCContractRequest) (*tradingpb.ExerciseOTCContractResponse, error) {
	res, err := s.Svc.ExerciseOTCContract(ctx, service.ExerciseOTCContractInput{ContractID: in.GetId()})
	if err != nil {
		return nil, err
	}
	return &tradingpb.ExerciseOTCContractResponse{
		Contract:                 otcContractToProto(s, ctx, res.Contract),
		StrikeOpId:               res.StrikeOpID,
		SellerRealizedGainNative: res.SellerRealizedGainNative,
		SellerRealizedGainRsd:    res.SellerRealizedGainRSD,
	}, nil
}

// =====================================================================
// proto conversion helpers
// =====================================================================

func otcOfferToProto(s *Server, ctx context.Context, o *domain.OTCOffer) *tradingpb.OTCOffer {
	if o == nil {
		return nil
	}
	out := &tradingpb.OTCOffer{
		Id:              o.ID,
		ThreadId:        o.ThreadID,
		SecurityId:      o.SecurityID,
		SellerHoldingId: o.SellerHoldingID,
		BuyerId:         o.BuyerID,
		BuyerKind:       userKindToProto(o.BuyerKind),
		BuyerAccountId:  o.BuyerAccountID,
		SellerId:        o.SellerID,
		SellerKind:      userKindToProto(o.SellerKind),
		SellerAccountId: o.SellerAccountID,
		Quantity:        o.Quantity,
		PricePerUnit:    o.PricePerUnit,
		Premium:         o.Premium,
		Currency:        currencyToProto(o.Currency),
		SettlementDate:  timestamppb.New(o.SettlementDate),
		ModifiedBy:      o.ModifiedBy,
		Status:          otcStatusToProto(o.Status),
		CreatedAt:       timestamppb.New(o.CreatedAt),
		UpdatedAt:       timestamppb.New(o.UpdatedAt),
	}
	if sec, err := s.Svc.Store.GetSecurity(ctx, o.SecurityID); err == nil {
		out.SecurityTicker = sec.Ticker
	}
	return out
}

func otcContractToProto(s *Server, ctx context.Context, c *domain.OTCContract) *tradingpb.OTCContract {
	if c == nil {
		return nil
	}
	out := &tradingpb.OTCContract{
		Id:              c.ID,
		ThreadId:        c.ThreadID,
		SecurityId:      c.SecurityID,
		SellerHoldingId: c.SellerHoldingID,
		BuyerId:         c.BuyerID,
		BuyerKind:       userKindToProto(c.BuyerKind),
		BuyerAccountId:  c.BuyerAccountID,
		SellerId:        c.SellerID,
		SellerKind:      userKindToProto(c.SellerKind),
		SellerAccountId: c.SellerAccountID,
		Quantity:        c.Quantity,
		StrikePrice:     c.StrikePrice,
		PremiumPaid:     c.PremiumPaid,
		Currency:        currencyToProto(c.Currency),
		SettlementDate:  timestamppb.New(c.SettlementDate),
		PremiumOpId:     c.PremiumOpID,
		Status:          otcContractStatusToProto(c.Status),
		ExercisedOpId:   c.ExercisedOpID,
		ExerciseSagaId:  c.ExerciseSagaID,
		CreatedAt:       timestamppb.New(c.CreatedAt),
		UpdatedAt:       timestamppb.New(c.UpdatedAt),
	}
	if c.ExercisedAt != nil {
		out.ExercisedAt = timestamppb.New(*c.ExercisedAt)
	}
	if sec, err := s.Svc.Store.GetSecurity(ctx, c.SecurityID); err == nil {
		out.SecurityTicker = sec.Ticker
	}
	// Resolve the seller's human-readable name the same way the OTC
	// discovery board does (service.ListPublicHoldings); without this
	// the FE "Sklopljeni ugovori" page falls back to the raw UUID.
	if s.Svc.Users != nil {
		if name, err := s.Svc.Users.DisplayName(ctx, c.SellerID, c.SellerKind); err == nil {
			out.SellerDisplayName = name
		}
	}
	return out
}

func otcStatusToProto(s domain.OTCStatus) tradingpb.OTCStatus {
	switch s {
	case domain.OTCStatusOpen:
		return tradingpb.OTCStatus_OTC_STATUS_OPEN
	case domain.OTCStatusSuperseded:
		return tradingpb.OTCStatus_OTC_STATUS_SUPERSEDED
	case domain.OTCStatusAccepted:
		return tradingpb.OTCStatus_OTC_STATUS_ACCEPTED
	case domain.OTCStatusWithdrawn:
		return tradingpb.OTCStatus_OTC_STATUS_WITHDRAWN
	case domain.OTCStatusExpired:
		return tradingpb.OTCStatus_OTC_STATUS_EXPIRED
	}
	return tradingpb.OTCStatus_OTC_STATUS_UNSPECIFIED
}

func otcContractStatusToProto(s domain.OTCContractStatus) tradingpb.OTCContractStatus {
	switch s {
	case domain.OTCContractActive:
		return tradingpb.OTCContractStatus_OTC_CONTRACT_STATUS_ACTIVE
	case domain.OTCContractExercised:
		return tradingpb.OTCContractStatus_OTC_CONTRACT_STATUS_EXERCISED
	case domain.OTCContractExpired:
		return tradingpb.OTCContractStatus_OTC_CONTRACT_STATUS_EXPIRED
	case domain.OTCContractSettling:
		return tradingpb.OTCContractStatus_OTC_CONTRACT_STATUS_SETTLING
	}
	return tradingpb.OTCContractStatus_OTC_CONTRACT_STATUS_UNSPECIFIED
}
