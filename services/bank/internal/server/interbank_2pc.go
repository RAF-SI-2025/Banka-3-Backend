// Inter-bank 2PC gRPC handlers (celina 5). Adapts the proto-generated
// InterbankProtocolService surface to the service layer.

package server

import (
	"context"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) PreparePayment(ctx context.Context, in *bankpb.PreparePaymentRequest) (*bankpb.PreparePaymentResponse, error) {
	res, err := s.Svc.PreparePayment(ctx, service.PreparePaymentInput{
		SenderRoutingNumber: int(in.GetSenderRoutingNumber()),
		TransactionID:       in.GetTransactionId(),
		Direction:           interbankDirectionFromProto(in.GetDirection()),
		LocalAccountNumber:  in.GetLocalAccountNumber(),
		RemoteAccountNumber: in.GetRemoteAccountNumber(),
		Currency:            currencyFromProto(in.GetCurrency()),
		Amount:              in.GetAmount(),
		TransactionBody:     in.GetTransactionBody(),
		Purpose:             in.GetPurpose(),
	})
	if err != nil {
		return nil, err
	}
	return &bankpb.PreparePaymentResponse{
		TransactionId: res.TransactionID,
		Status:        interbankTxStatusToProto(res.Status),
		ReservationId: res.ReservationID,
		PreparedAt:    timestamppb.Now(),
	}, nil
}

func (s *Server) CommitPayment(ctx context.Context, in *bankpb.CommitPaymentRequest) (*bankpb.CommitPaymentResponse, error) {
	res, err := s.Svc.CommitPayment(ctx, int(in.GetSenderRoutingNumber()), in.GetTransactionId())
	if err != nil {
		return nil, err
	}
	return &bankpb.CommitPaymentResponse{
		TransactionId: res.TransactionID,
		Status:        interbankTxStatusToProto(res.Status),
		OpId:          res.OpID,
		CommittedAt:   timestamppb.Now(),
	}, nil
}

func (s *Server) RollbackPayment(ctx context.Context, in *bankpb.RollbackPaymentRequest) (*bankpb.RollbackPaymentResponse, error) {
	res, err := s.Svc.RollbackPayment(ctx, int(in.GetSenderRoutingNumber()), in.GetTransactionId(), in.GetReason())
	if err != nil {
		return nil, err
	}
	return &bankpb.RollbackPaymentResponse{
		TransactionId: res.TransactionID,
		Status:        interbankTxStatusToProto(res.Status),
		RolledBackAt:  timestamppb.Now(),
	}, nil
}

func (s *Server) RecordInboundMessage(ctx context.Context, in *bankpb.RecordInboundMessageRequest) (*bankpb.RecordInboundMessageResponse, error) {
	if err := s.Svc.RecordInboundMessage(ctx, service.RecordInboundMessageInput{
		SenderRoutingNumber: int(in.GetSenderRoutingNumber()),
		IdempotenceKey:      in.GetIdempotenceKey(),
		MessageType:         interbankMessageTypeFromProto(in.GetMessageType()),
		TransactionID:       in.GetTransactionId(),
		ResponseStatus:      int(in.GetResponseStatus()),
		ResponseBody:        in.GetResponseBody(),
	}); err != nil {
		return nil, err
	}
	return &bankpb.RecordInboundMessageResponse{RecordedAt: timestamppb.Now()}, nil
}

func (s *Server) GetInboundMessage(ctx context.Context, in *bankpb.GetInboundMessageRequest) (*bankpb.GetInboundMessageResponse, error) {
	row, err := s.Svc.GetInboundMessage(ctx, int(in.GetSenderRoutingNumber()), in.GetIdempotenceKey())
	if err != nil {
		return nil, err
	}
	if row == nil {
		return &bankpb.GetInboundMessageResponse{Found: false}, nil
	}
	return &bankpb.GetInboundMessageResponse{
		Found:          true,
		MessageType:    interbankMessageTypeToProto(row.MessageType),
		TransactionId:  row.TransactionID,
		ResponseStatus: int32(row.ResponseStatus),
		ResponseBody:   row.ResponseBody,
		RecordedAt:     timestamppb.New(row.CreatedAt),
		UpdatedAt:      timestamppb.New(row.UpdatedAt),
	}, nil
}

// =====================================================================
// proto conversion helpers.
// =====================================================================

func interbankDirectionFromProto(d bankpb.InterbankPaymentDirection) domain.InterbankPaymentDirection {
	switch d {
	case bankpb.InterbankPaymentDirection_INTERBANK_PAYMENT_DIRECTION_INBOUND:
		return domain.InterbankInbound
	case bankpb.InterbankPaymentDirection_INTERBANK_PAYMENT_DIRECTION_OUTBOUND:
		return domain.InterbankOutbound
	}
	return ""
}

func interbankTxStatusToProto(s domain.InterbankTxStatus) bankpb.InterbankTxStatus {
	switch s {
	case domain.InterbankTxPrepared:
		return bankpb.InterbankTxStatus_INTERBANK_TX_STATUS_PREPARED
	case domain.InterbankTxCommitted:
		return bankpb.InterbankTxStatus_INTERBANK_TX_STATUS_COMMITTED
	case domain.InterbankTxRolledBack:
		return bankpb.InterbankTxStatus_INTERBANK_TX_STATUS_ROLLED_BACK
	}
	return bankpb.InterbankTxStatus_INTERBANK_TX_STATUS_UNSPECIFIED
}

func interbankMessageTypeFromProto(t bankpb.InterbankMessageType) domain.InterbankMessageType {
	switch t {
	case bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_NEW_TX:
		return domain.InterbankMsgNewTx
	case bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_COMMIT_TX:
		return domain.InterbankMsgCommitTx
	case bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_ROLLBACK_TX:
		return domain.InterbankMsgRollbackTx
	}
	return ""
}

func interbankMessageTypeToProto(t domain.InterbankMessageType) bankpb.InterbankMessageType {
	switch t {
	case domain.InterbankMsgNewTx:
		return bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_NEW_TX
	case domain.InterbankMsgCommitTx:
		return bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_COMMIT_TX
	case domain.InterbankMsgRollbackTx:
		return bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_ROLLBACK_TX
	}
	return bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_UNSPECIFIED
}
