// Inter-bank 2PC gRPC handlers (celina 5). Adapts the proto-generated
// InterbankProtocolService surface to the service layer.

package server

import (
	"context"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) PreparePayment(ctx context.Context, in *bankpb.PreparePaymentRequest) (*bankpb.PreparePaymentResponse, error) {
	log := logger.From(ctx)
	dir := interbankDirectionFromProto(in.GetDirection())
	res, err := s.Svc.PreparePayment(ctx, service.PreparePaymentInput{
		SenderRoutingNumber: int(in.GetSenderRoutingNumber()),
		TransactionID:       in.GetTransactionId(),
		Direction:           dir,
		LocalAccountNumber:  in.GetLocalAccountNumber(),
		RemoteAccountNumber: in.GetRemoteAccountNumber(),
		Currency:            currencyFromProto(in.GetCurrency()),
		Amount:              in.GetAmount(),
		TransactionBody:     in.GetTransactionBody(),
		Purpose:             in.GetPurpose(),
	})
	if err != nil {
		log.ErrorContext(ctx, "prepare payment failed", "err", err, "transaction_id", in.GetTransactionId(), "sender_routing", in.GetSenderRoutingNumber(), "direction", string(dir))
		return nil, err
	}
	log.InfoContext(ctx, "interbank payment prepared", "transaction_id", res.TransactionID, "reservation_id", res.ReservationID, "direction", string(dir), "sender_routing", in.GetSenderRoutingNumber(), "status", string(res.Status))
	return &bankpb.PreparePaymentResponse{
		TransactionId: res.TransactionID,
		Status:        interbankTxStatusToProto(res.Status),
		ReservationId: res.ReservationID,
		PreparedAt:    timestamppb.Now(),
	}, nil
}

func (s *Server) CommitPayment(ctx context.Context, in *bankpb.CommitPaymentRequest) (*bankpb.CommitPaymentResponse, error) {
	log := logger.From(ctx)
	res, err := s.Svc.CommitPayment(ctx, int(in.GetSenderRoutingNumber()), in.GetTransactionId())
	if err != nil {
		log.ErrorContext(ctx, "commit payment failed", "err", err, "transaction_id", in.GetTransactionId(), "sender_routing", in.GetSenderRoutingNumber())
		return nil, err
	}
	log.InfoContext(ctx, "interbank payment committed", "transaction_id", res.TransactionID, "op_id", res.OpID, "sender_routing", in.GetSenderRoutingNumber(), "status", string(res.Status))
	return &bankpb.CommitPaymentResponse{
		TransactionId: res.TransactionID,
		Status:        interbankTxStatusToProto(res.Status),
		OpId:          res.OpID,
		CommittedAt:   timestamppb.Now(),
	}, nil
}

func (s *Server) RollbackPayment(ctx context.Context, in *bankpb.RollbackPaymentRequest) (*bankpb.RollbackPaymentResponse, error) {
	log := logger.From(ctx)
	res, err := s.Svc.RollbackPayment(ctx, int(in.GetSenderRoutingNumber()), in.GetTransactionId(), in.GetReason())
	if err != nil {
		log.ErrorContext(ctx, "rollback payment failed", "err", err, "transaction_id", in.GetTransactionId(), "sender_routing", in.GetSenderRoutingNumber(), "reason", in.GetReason())
		return nil, err
	}
	log.InfoContext(ctx, "interbank payment rolled back", "transaction_id", res.TransactionID, "sender_routing", in.GetSenderRoutingNumber(), "status", string(res.Status), "reason", in.GetReason())
	return &bankpb.RollbackPaymentResponse{
		TransactionId: res.TransactionID,
		Status:        interbankTxStatusToProto(res.Status),
		RolledBackAt:  timestamppb.Now(),
	}, nil
}

func (s *Server) RecordInboundMessage(ctx context.Context, in *bankpb.RecordInboundMessageRequest) (*bankpb.RecordInboundMessageResponse, error) {
	mt := interbankMessageTypeFromProto(in.GetMessageType())
	if err := s.Svc.RecordInboundMessage(ctx, service.RecordInboundMessageInput{
		SenderRoutingNumber: int(in.GetSenderRoutingNumber()),
		IdempotenceKey:      in.GetIdempotenceKey(),
		MessageType:         mt,
		TransactionID:       in.GetTransactionId(),
		ResponseStatus:      int(in.GetResponseStatus()),
		ResponseBody:        in.GetResponseBody(),
	}); err != nil {
		logger.From(ctx).ErrorContext(ctx, "record inbound message failed", "err", err, "transaction_id", in.GetTransactionId(), "sender_routing", in.GetSenderRoutingNumber(), "idempotence_key", in.GetIdempotenceKey(), "message_type", string(mt))
		return nil, err
	}
	return &bankpb.RecordInboundMessageResponse{RecordedAt: timestamppb.Now()}, nil
}

func (s *Server) GetInboundMessage(ctx context.Context, in *bankpb.GetInboundMessageRequest) (*bankpb.GetInboundMessageResponse, error) {
	row, err := s.Svc.GetInboundMessage(ctx, int(in.GetSenderRoutingNumber()), in.GetIdempotenceKey())
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "get inbound message failed", "err", err, "sender_routing", in.GetSenderRoutingNumber(), "idempotence_key", in.GetIdempotenceKey())
		return nil, err
	}
	if row == nil {
		return &bankpb.GetInboundMessageResponse{Found: false}, nil
	}
	logger.From(ctx).WarnContext(ctx, "interbank message replay detected", "transaction_id", row.TransactionID, "sender_routing", in.GetSenderRoutingNumber(), "idempotence_key", in.GetIdempotenceKey(), "message_type", string(row.MessageType))
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
