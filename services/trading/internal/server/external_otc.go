// External OTC gRPC handlers (celina 5). Mirrors server/otc.go but
// adapts the ExternalOTCService surface to the cross-bank service.
// Both surfaces are registered on the same Server (see server.go).

package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// =====================================================================
// Discovery
// =====================================================================

func (s *Server) ListExternalPublicHoldings(ctx context.Context, in *tradingpb.ListExternalPublicHoldingsRequest) (*tradingpb.ListExternalPublicHoldingsResponse, error) {
	rows, err := s.Svc.ListExternalPublicHoldings(ctx, in.GetBankCode(), in.GetTicker())
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListExternalPublicHoldingsResponse{
		Items: make([]*tradingpb.ExternalPublicHolding, 0, len(rows)),
	}
	for _, r := range rows {
		out.Items = append(out.Items, &tradingpb.ExternalPublicHolding{
			BankCode:        r.BankCode,
			SellerUserRef:   r.SellerUserRef,
			SellerDisplay:   r.SellerDisplay,
			SellerHoldingId: r.SellerHoldingRef,
			SecurityTicker:  r.SecurityTicker,
			SecurityType:    securityTypeToProto(r.SecurityType),
			Currency:        currencyToProto(r.Currency),
			Quantity:        r.Quantity,
			AskPrice:        r.AskPrice,
			Premium:         r.Premium,
		})
	}
	return out, nil
}

// =====================================================================
// Outbound — user-facing threads.
// =====================================================================

func (s *Server) CreateExternalOTCOffer(ctx context.Context, in *tradingpb.CreateExternalOTCOfferRequest) (*tradingpb.CreateExternalOTCOfferResponse, error) {
	t, err := s.Svc.CreateExternalOTCOffer(ctx, service.CreateExternalOTCOfferInput{
		RemoteBankCode:    in.GetRemoteBankCode(),
		RemoteUserRef:     in.GetRemoteUserRef(),
		RemoteDisplayName: in.GetRemoteDisplayName(),
		BuyerAccountID:    in.GetBuyerAccountId(),
		SellerHoldingRef:  in.GetSellerHoldingId(),
		SecurityTicker:    in.GetSecurityTicker(),
		SecurityType:      securityTypeFromProto(in.GetSecurityType()),
		Currency:          currencyFromProto(in.GetCurrency()),
		Quantity:          in.GetQuantity(),
		PricePerUnit:      in.GetPricePerUnit(),
		Premium:           in.GetPremium(),
		SettlementDate:    in.GetSettlementDate().AsTime(),
	})
	if err != nil {
		return nil, err
	}
	return &tradingpb.CreateExternalOTCOfferResponse{LocalMirror: externalOTCThreadToProto(t)}, nil
}

func (s *Server) ListExternalOTCThreads(ctx context.Context, in *tradingpb.ListExternalOTCThreadsRequest) (*tradingpb.ListExternalOTCThreadsResponse, error) {
	rows, err := s.Svc.ListExternalOTCThreads(ctx, externalOTCThreadStatusFromProto(in.GetStatus()))
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListExternalOTCThreadsResponse{Threads: make([]*tradingpb.ExternalOTCThread, 0, len(rows))}
	for _, t := range rows {
		out.Threads = append(out.Threads, externalOTCThreadToProto(t))
	}
	return out, nil
}

func (s *Server) GetExternalOTCThread(ctx context.Context, in *tradingpb.GetExternalOTCThreadRequest) (*tradingpb.GetExternalOTCThreadResponse, error) {
	res, err := s.Svc.GetExternalOTCThread(ctx, in.GetThreadId())
	if err != nil {
		return nil, err
	}
	out := &tradingpb.GetExternalOTCThreadResponse{
		Thread:     externalOTCThreadToProto(res.Thread),
		Iterations: make([]*tradingpb.ExternalOTCIteration, 0, len(res.Iterations)),
	}
	for _, it := range res.Iterations {
		out.Iterations = append(out.Iterations, externalOTCIterationToProto(it))
	}
	if res.Contract != nil {
		out.Contract = externalOTCContractToProto(res.Contract)
	}
	return out, nil
}

func (s *Server) CounterExternalOTCOffer(ctx context.Context, in *tradingpb.CounterExternalOTCOfferRequest) (*tradingpb.ExternalOTCThread, error) {
	t, err := s.Svc.CounterExternalOTCOffer(ctx, service.CounterExternalOTCOfferInput{
		BankCode:       in.GetBankCode(),
		ThreadID:       in.GetThreadId(),
		Quantity:       in.GetQuantity(),
		PricePerUnit:   in.GetPricePerUnit(),
		Premium:        in.GetPremium(),
		SettlementDate: in.GetSettlementDate().AsTime(),
	})
	if err != nil {
		return nil, err
	}
	return externalOTCThreadToProto(t), nil
}

func (s *Server) WithdrawExternalOTCOffer(ctx context.Context, in *tradingpb.WithdrawExternalOTCOfferRequest) (*tradingpb.ExternalOTCThread, error) {
	t, err := s.Svc.WithdrawExternalOTCOffer(ctx, in.GetBankCode(), in.GetThreadId())
	if err != nil {
		return nil, err
	}
	return externalOTCThreadToProto(t), nil
}

func (s *Server) AcceptExternalOTCOffer(ctx context.Context, in *tradingpb.AcceptExternalOTCOfferRequest) (*tradingpb.AcceptExternalOTCOfferResponse, error) {
	res, err := s.Svc.AcceptExternalOTCOffer(ctx, in.GetBankCode(), in.GetThreadId())
	if err != nil {
		return nil, err
	}
	return &tradingpb.AcceptExternalOTCOfferResponse{
		Thread:   externalOTCThreadToProto(res.Thread),
		Contract: externalOTCContractToProto(res.Contract),
	}, nil
}

// =====================================================================
// Outbound — contracts.
// =====================================================================

func (s *Server) ListExternalOTCContracts(ctx context.Context, in *tradingpb.ListExternalOTCContractsRequest) (*tradingpb.ListExternalOTCContractsResponse, error) {
	rows, err := s.Svc.ListExternalOTCContracts(ctx, externalOTCContractStatusFromProto(in.GetStatus()))
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListExternalOTCContractsResponse{Contracts: make([]*tradingpb.ExternalOTCContract, 0, len(rows))}
	for _, c := range rows {
		out.Contracts = append(out.Contracts, externalOTCContractToProto(c))
	}
	return out, nil
}

func (s *Server) ExerciseExternalOTCContract(ctx context.Context, in *tradingpb.ExerciseExternalOTCContractRequest) (*tradingpb.ExerciseExternalOTCContractResponse, error) {
	c, err := s.Svc.ExerciseExternalOTCContract(ctx, in.GetBankCode(), in.GetContractId(), in.GetExerciseOpId())
	if err != nil {
		return nil, err
	}
	return &tradingpb.ExerciseExternalOTCContractResponse{Contract: externalOTCContractToProto(c)}, nil
}

// =====================================================================
// Inbound — gateway-driven.
// =====================================================================

func (s *Server) ReceiveExternalOTCOffer(ctx context.Context, in *tradingpb.ReceiveExternalOTCOfferRequest) (*tradingpb.ReceiveExternalOTCOfferResponse, error) {
	t, err := s.Svc.ReceiveExternalOTCOffer(ctx, service.ReceiveExternalOTCOfferInput{
		SenderBankCode:    in.GetSenderBankCode(),
		SenderUserRef:     in.GetSenderUserRef(),
		SenderDisplayName: in.GetSenderDisplayName(),
		SenderThreadID:    in.GetSenderThreadId(),
		SellerHoldingRef:  in.GetSellerHoldingId(),
		Quantity:          in.GetQuantity(),
		PricePerUnit:      in.GetPricePerUnit(),
		Premium:           in.GetPremium(),
		SettlementDate:    in.GetSettlementDate().AsTime(),
	})
	if err != nil {
		return nil, err
	}
	return &tradingpb.ReceiveExternalOTCOfferResponse{LocalMirror: externalOTCThreadToProto(t)}, nil
}

func (s *Server) ReceiveExternalOTCCounter(ctx context.Context, in *tradingpb.ReceiveExternalOTCCounterRequest) (*tradingpb.ExternalOTCThread, error) {
	t, err := s.Svc.ReceiveExternalOTCCounter(ctx, service.ReceiveExternalOTCCounterInput{
		SenderBankCode: in.GetSenderBankCode(),
		SenderThreadID: in.GetSenderThreadId(),
		Quantity:       in.GetQuantity(),
		PricePerUnit:   in.GetPricePerUnit(),
		Premium:        in.GetPremium(),
		SettlementDate: in.GetSettlementDate().AsTime(),
	})
	if err != nil {
		return nil, err
	}
	return externalOTCThreadToProto(t), nil
}

func (s *Server) ReceiveExternalOTCWithdraw(ctx context.Context, in *tradingpb.ReceiveExternalOTCActionRequest) (*tradingpb.ExternalOTCThread, error) {
	t, err := s.Svc.ReceiveExternalOTCWithdraw(ctx, service.ReceiveExternalOTCAction{
		SenderBankCode: in.GetSenderBankCode(),
		SenderThreadID: in.GetSenderThreadId(),
	})
	if err != nil {
		return nil, err
	}
	return externalOTCThreadToProto(t), nil
}

func (s *Server) ReceiveExternalOTCAccept(ctx context.Context, in *tradingpb.ReceiveExternalOTCActionRequest) (*tradingpb.ExternalOTCThread, error) {
	t, err := s.Svc.ReceiveExternalOTCAccept(ctx, service.ReceiveExternalOTCAction{
		SenderBankCode: in.GetSenderBankCode(),
		SenderThreadID: in.GetSenderThreadId(),
	})
	if err != nil {
		return nil, err
	}
	return externalOTCThreadToProto(t), nil
}

func (s *Server) ReceiveExternalOTCExerciseNotice(ctx context.Context, in *tradingpb.ReceiveExternalOTCExerciseNoticeRequest) (*tradingpb.ReceiveExternalOTCExerciseNoticeResponse, error) {
	c, err := s.Svc.ReceiveExternalOTCExerciseNotice(ctx, service.ReceiveExternalOTCExerciseNoticeInput{
		SenderBankCode:   in.GetSenderBankCode(),
		SenderContractID: in.GetSenderContractId(),
		ExerciseOpID:     in.GetExerciseOpId(),
	})
	if err != nil {
		return nil, err
	}
	return &tradingpb.ReceiveExternalOTCExerciseNoticeResponse{Contract: externalOTCContractToProto(c)}, nil
}

var settlementPhaseToString = map[tradingpb.ExternalOTCSettlementPhase]string{
	tradingpb.ExternalOTCSettlementPhase_EXTERNAL_OTC_SETTLEMENT_PHASE_PREPARE:  "prepare",
	tradingpb.ExternalOTCSettlementPhase_EXTERNAL_OTC_SETTLEMENT_PHASE_COMMIT:   "commit",
	tradingpb.ExternalOTCSettlementPhase_EXTERNAL_OTC_SETTLEMENT_PHASE_ROLLBACK: "rollback",
}

var settlementKindToString = map[tradingpb.ExternalOTCSettlementKind]string{
	tradingpb.ExternalOTCSettlementKind_EXTERNAL_OTC_SETTLEMENT_KIND_ACCEPT:   "accept",
	tradingpb.ExternalOTCSettlementKind_EXTERNAL_OTC_SETTLEMENT_KIND_EXERCISE: "exercise",
}

func (s *Server) SettleExternalOTCOption(ctx context.Context, in *tradingpb.SettleExternalOTCOptionRequest) (*tradingpb.SettleExternalOTCOptionResponse, error) {
	res, err := s.Svc.SettleExternalOTCOption(ctx, service.SettleExternalOTCOptionInput{
		Phase:          settlementPhaseToString[in.GetPhase()],
		Kind:           settlementKindToString[in.GetKind()],
		SenderBankCode: in.GetSenderBankCode(),
		TransactionID:  in.GetTransactionId(),
		OptionRef:      in.GetOptionRef(),
		SellerUserRef:  in.GetSellerUserRef(),
		CashAmount:     in.GetCashAmount(),
		CashCurrency:   in.GetCashCurrency(),
		Ticker:         in.GetTicker(),
		Quantity:       in.GetQuantity(),
	})
	if err != nil {
		return nil, err
	}
	return &tradingpb.SettleExternalOTCOptionResponse{
		Accepted:            res.Accepted,
		Reason:              res.Reason,
		Handled:             res.Handled,
		SellerAccountNumber: res.SellerAccountNumber,
	}, nil
}

// =====================================================================
// Proto conversion helpers
// =====================================================================

func externalOTCThreadToProto(t *domain.ExternalOTCThread) *tradingpb.ExternalOTCThread {
	if t == nil {
		return nil
	}
	return &tradingpb.ExternalOTCThread{
		Id:                 t.ID,
		Direction:          externalOTCDirectionToProto(t.Direction),
		RemoteBankCode:     t.RemoteBankCode,
		RemoteThreadId:     t.RemoteThreadID,
		RemoteUserRef:      t.RemoteUserRef,
		RemoteDisplayName:  t.RemoteDisplayName,
		RemoteAccountRef:   t.RemoteAccountRef,
		LocalUserId:        t.LocalUserID,
		LocalUserKind:      userKindToProto(t.LocalUserKind),
		LocalAccountId:     t.LocalAccountID,
		LocalAccountNumber: t.LocalAccountNumber,
		LocalRole:          externalOTCRoleToProto(t.LocalRole),
		SecurityId:         t.SecurityID,
		SecurityTicker:     t.SecurityTicker,
		SellerHoldingId:    t.SellerHoldingRef,
		Quantity:           t.Quantity,
		PricePerUnit:       t.PricePerUnit,
		Premium:            t.Premium,
		Currency:           currencyToProto(t.Currency),
		SettlementDate:     timestamppb.New(t.SettlementDate),
		ModifiedBySide:     externalOTCSideToProto(t.ModifiedBySide),
		Status:             externalOTCThreadStatusToProto(t.Status),
		CreatedAt:          timestamppb.New(t.CreatedAt),
		UpdatedAt:          timestamppb.New(t.UpdatedAt),
	}
}

func externalOTCIterationToProto(it *domain.ExternalOTCIteration) *tradingpb.ExternalOTCIteration {
	if it == nil {
		return nil
	}
	return &tradingpb.ExternalOTCIteration{
		Id:             it.ID,
		ThreadId:       it.ThreadID,
		ProposedBySide: externalOTCSideToProto(it.ProposedBySide),
		Quantity:       it.Quantity,
		PricePerUnit:   it.PricePerUnit,
		Premium:        it.Premium,
		SettlementDate: timestamppb.New(it.SettlementDate),
		CreatedAt:      timestamppb.New(it.CreatedAt),
	}
}

func externalOTCContractToProto(c *domain.ExternalOTCContract) *tradingpb.ExternalOTCContract {
	if c == nil {
		return nil
	}
	out := &tradingpb.ExternalOTCContract{
		Id:                 c.ID,
		ThreadId:           c.ThreadID,
		Direction:          externalOTCDirectionToProto(c.Direction),
		RemoteBankCode:     c.RemoteBankCode,
		RemoteThreadId:     c.RemoteThreadID,
		RemoteUserRef:      c.RemoteUserRef,
		RemoteDisplayName:  c.RemoteDisplayName,
		RemoteAccountRef:   c.RemoteAccountRef,
		LocalUserId:        c.LocalUserID,
		LocalUserKind:      userKindToProto(c.LocalUserKind),
		LocalAccountId:     c.LocalAccountID,
		LocalAccountNumber: c.LocalAccountNumber,
		LocalRole:          externalOTCRoleToProto(c.LocalRole),
		SecurityId:         c.SecurityID,
		SecurityTicker:     c.SecurityTicker,
		SellerHoldingId:    c.SellerHoldingRef,
		Quantity:           c.Quantity,
		StrikePrice:        c.StrikePrice,
		PremiumPaid:        c.PremiumPaid,
		Currency:           currencyToProto(c.Currency),
		SettlementDate:     timestamppb.New(c.SettlementDate),
		AcceptedBySide:     externalOTCSideToProto(c.AcceptedBySide),
		Status:             externalOTCContractStatusToProto(c.Status),
		PremiumOpId:        c.PremiumOpID,
		ExerciseOpId:       c.ExerciseOpID,
		CreatedAt:          timestamppb.New(c.CreatedAt),
		UpdatedAt:          timestamppb.New(c.UpdatedAt),
	}
	if c.ExercisedAt != nil {
		out.ExercisedAt = timestamppb.New(*c.ExercisedAt)
	}
	return out
}

func externalOTCDirectionToProto(d domain.ExternalOTCDirection) tradingpb.ExternalOTCDirection {
	switch d {
	case domain.ExternalOTCOutgoing:
		return tradingpb.ExternalOTCDirection_EXTERNAL_OTC_DIRECTION_OUTGOING
	case domain.ExternalOTCIncoming:
		return tradingpb.ExternalOTCDirection_EXTERNAL_OTC_DIRECTION_INCOMING
	}
	return tradingpb.ExternalOTCDirection_EXTERNAL_OTC_DIRECTION_UNSPECIFIED
}

func externalOTCSideToProto(s domain.ExternalOTCSide) tradingpb.ExternalOTCSide {
	switch s {
	case domain.ExternalOTCSideLocal:
		return tradingpb.ExternalOTCSide_EXTERNAL_OTC_SIDE_LOCAL
	case domain.ExternalOTCSideRemote:
		return tradingpb.ExternalOTCSide_EXTERNAL_OTC_SIDE_REMOTE
	}
	return tradingpb.ExternalOTCSide_EXTERNAL_OTC_SIDE_UNSPECIFIED
}

func externalOTCRoleToProto(r domain.ExternalOTCRole) tradingpb.ExternalOTCRole {
	switch r {
	case domain.ExternalOTCRoleBuyer:
		return tradingpb.ExternalOTCRole_EXTERNAL_OTC_ROLE_BUYER
	case domain.ExternalOTCRoleSeller:
		return tradingpb.ExternalOTCRole_EXTERNAL_OTC_ROLE_SELLER
	}
	return tradingpb.ExternalOTCRole_EXTERNAL_OTC_ROLE_UNSPECIFIED
}

func externalOTCThreadStatusToProto(s domain.ExternalOTCThreadStatus) tradingpb.ExternalOTCThreadStatus {
	switch s {
	case domain.ExternalOTCThreadOpen:
		return tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_OPEN
	case domain.ExternalOTCThreadSuperseded:
		return tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_SUPERSEDED
	case domain.ExternalOTCThreadAccepted:
		return tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_ACCEPTED
	case domain.ExternalOTCThreadWithdrawn:
		return tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_WITHDRAWN
	case domain.ExternalOTCThreadExpired:
		return tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_EXPIRED
	case domain.ExternalOTCThreadRejected:
		return tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_REJECTED
	}
	return tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_UNSPECIFIED
}

func externalOTCThreadStatusFromProto(s tradingpb.ExternalOTCThreadStatus) string {
	switch s {
	case tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_OPEN:
		return string(domain.ExternalOTCThreadOpen)
	case tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_SUPERSEDED:
		return string(domain.ExternalOTCThreadSuperseded)
	case tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_ACCEPTED:
		return string(domain.ExternalOTCThreadAccepted)
	case tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_WITHDRAWN:
		return string(domain.ExternalOTCThreadWithdrawn)
	case tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_EXPIRED:
		return string(domain.ExternalOTCThreadExpired)
	case tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_REJECTED:
		return string(domain.ExternalOTCThreadRejected)
	}
	return ""
}

func externalOTCContractStatusToProto(s domain.ExternalOTCContractStatus) tradingpb.ExternalOTCContractStatus {
	switch s {
	case domain.ExternalOTCContractActive:
		return tradingpb.ExternalOTCContractStatus_EXTERNAL_OTC_CONTRACT_STATUS_ACTIVE
	case domain.ExternalOTCContractExercised:
		return tradingpb.ExternalOTCContractStatus_EXTERNAL_OTC_CONTRACT_STATUS_EXERCISED
	case domain.ExternalOTCContractExpired:
		return tradingpb.ExternalOTCContractStatus_EXTERNAL_OTC_CONTRACT_STATUS_EXPIRED
	case domain.ExternalOTCContractSettling:
		return tradingpb.ExternalOTCContractStatus_EXTERNAL_OTC_CONTRACT_STATUS_SETTLING
	}
	return tradingpb.ExternalOTCContractStatus_EXTERNAL_OTC_CONTRACT_STATUS_UNSPECIFIED
}

func externalOTCContractStatusFromProto(s tradingpb.ExternalOTCContractStatus) string {
	switch s {
	case tradingpb.ExternalOTCContractStatus_EXTERNAL_OTC_CONTRACT_STATUS_ACTIVE:
		return string(domain.ExternalOTCContractActive)
	case tradingpb.ExternalOTCContractStatus_EXTERNAL_OTC_CONTRACT_STATUS_EXERCISED:
		return string(domain.ExternalOTCContractExercised)
	case tradingpb.ExternalOTCContractStatus_EXTERNAL_OTC_CONTRACT_STATUS_EXPIRED:
		return string(domain.ExternalOTCContractExpired)
	case tradingpb.ExternalOTCContractStatus_EXTERNAL_OTC_CONTRACT_STATUS_SETTLING:
		return string(domain.ExternalOTCContractSettling)
	}
	return ""
}
