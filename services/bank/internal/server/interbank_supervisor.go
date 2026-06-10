// Inter-bank observability & control gRPC handlers (celina 5).
// Adapt the supervisor read/control surface on BankService to the
// service layer.

package server

import (
	"context"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) ListInterbankTransactions(ctx context.Context, in *bankpb.ListInterbankTransactionsRequest) (*bankpb.ListInterbankTransactionsResponse, error) {
	txs, total, err := s.Svc.ListInterbankTransactions(ctx, service.ListInterbankTransactionsInput{
		SenderRoutingNumber: int(in.GetSenderRoutingNumber()),
		Status:              domain.InterbankTxStatus(in.GetStatus()),
		Direction:           domain.InterbankPaymentDirection(in.GetDirection()),
		From:                tsOrZero(in.GetFrom()),
		To:                  tsOrZero(in.GetTo()),
		Page:                int(in.GetPage()),
		PageSize:            int(in.GetPageSize()),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.InterbankTransaction, 0, len(txs))
	for _, t := range txs {
		out = append(out, interbankTxToProto(t))
	}
	page, pageSize := pageEcho(int(in.GetPage()), int(in.GetPageSize()))
	return &bankpb.ListInterbankTransactionsResponse{
		Transactions: out,
		Page:         int32(page),
		PageSize:     int32(pageSize),
		Total:        total,
	}, nil
}

func (s *Server) GetInterbankTransaction(ctx context.Context, in *bankpb.GetInterbankTransactionRequest) (*bankpb.InterbankTransaction, error) {
	t, err := s.Svc.GetInterbankTransaction(ctx, int(in.GetSenderRoutingNumber()), in.GetTransactionId())
	if err != nil {
		return nil, err
	}
	return interbankTxToProto(t), nil
}

func (s *Server) ListInterbankAuditLog(ctx context.Context, in *bankpb.ListInterbankAuditLogRequest) (*bankpb.ListInterbankAuditLogResponse, error) {
	msgs, total, err := s.Svc.ListInterbankAuditLog(ctx, service.ListInterbankAuditLogInput{
		SenderRoutingNumber: int(in.GetSenderRoutingNumber()),
		MessageType:         domain.InterbankMessageType(in.GetMessageType()),
		From:                tsOrZero(in.GetFrom()),
		To:                  tsOrZero(in.GetTo()),
		Page:                int(in.GetPage()),
		PageSize:            int(in.GetPageSize()),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.InterbankMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, interbankMsgToProto(m))
	}
	page, pageSize := pageEcho(int(in.GetPage()), int(in.GetPageSize()))
	return &bankpb.ListInterbankAuditLogResponse{
		Messages: out,
		Page:     int32(page),
		PageSize: int32(pageSize),
		Total:    total,
	}, nil
}

func (s *Server) ListInterbankBlacklist(ctx context.Context, in *bankpb.ListInterbankBlacklistRequest) (*bankpb.ListInterbankBlacklistResponse, error) {
	entries, err := s.Svc.ListInterbankBlacklist(ctx, in.GetActiveOnly())
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.InterbankBlacklistEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, blacklistEntryToProto(e))
	}
	return &bankpb.ListInterbankBlacklistResponse{Entries: out}, nil
}

func (s *Server) BlockInterbankPartner(ctx context.Context, in *bankpb.BlockInterbankPartnerRequest) (*bankpb.InterbankBlacklistEntry, error) {
	e, err := s.Svc.BlockInterbankPartner(ctx, int(in.GetSenderRoutingNumber()), in.GetReason())
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "block interbank partner failed", "err", err, "sender_routing", in.GetSenderRoutingNumber(), "reason", in.GetReason())
		return nil, err
	}
	logger.From(ctx).InfoContext(ctx, "interbank partner blocked", "sender_routing", in.GetSenderRoutingNumber(), "reason", in.GetReason())
	return blacklistEntryToProto(e), nil
}

func (s *Server) UnblockInterbankPartner(ctx context.Context, in *bankpb.UnblockInterbankPartnerRequest) (*bankpb.InterbankBlacklistEntry, error) {
	e, err := s.Svc.UnblockInterbankPartner(ctx, int(in.GetSenderRoutingNumber()))
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "unblock interbank partner failed", "err", err, "sender_routing", in.GetSenderRoutingNumber())
		return nil, err
	}
	logger.From(ctx).InfoContext(ctx, "interbank partner unblocked", "sender_routing", in.GetSenderRoutingNumber())
	return blacklistEntryToProto(e), nil
}

// =====================================================================
// conversions.
// =====================================================================

func interbankTxToProto(t *domain.InterbankProtocolTransaction) *bankpb.InterbankTransaction {
	return &bankpb.InterbankTransaction{
		SenderRoutingNumber: int32(t.SenderRoutingNumber),
		TransactionId:       t.TransactionID,
		Direction:           string(t.Direction),
		LocalAccountNumber:  t.LocalAccountNumber,
		RemoteAccountNumber: t.RemoteAccountNumber,
		Currency:            currencyToProto(t.Currency),
		Amount:              t.Amount,
		Purpose:             t.Purpose,
		ReservationId:       t.ReservationID,
		OpId:                t.OpID,
		Status:              string(t.Status),
		LastError:           t.LastError,
		CreatedAt:           timestamppb.New(t.CreatedAt),
		UpdatedAt:           timestamppb.New(t.UpdatedAt),
	}
}

func interbankMsgToProto(m *domain.InterbankProtocolMessage) *bankpb.InterbankMessage {
	return &bankpb.InterbankMessage{
		SenderRoutingNumber: int32(m.SenderRoutingNumber),
		IdempotenceKey:      m.IdempotenceKey,
		MessageType:         string(m.MessageType),
		TransactionId:       m.TransactionID,
		ResponseStatus:      int32(m.ResponseStatus),
		ResponseBody:        m.ResponseBody,
		CreatedAt:           timestamppb.New(m.CreatedAt),
		UpdatedAt:           timestamppb.New(m.UpdatedAt),
	}
}

func blacklistEntryToProto(e *domain.InterbankBlacklistEntry) *bankpb.InterbankBlacklistEntry {
	out := &bankpb.InterbankBlacklistEntry{
		SenderRoutingNumber: int32(e.SenderRoutingNumber),
		Reason:              e.Reason,
		BlockedBy:           e.BlockedBy,
		BlockedAt:           timestamppb.New(e.BlockedAt),
		Active:              e.Active,
	}
	if e.UnblockedAt != nil {
		out.UnblockedAt = timestamppb.New(*e.UnblockedAt)
	}
	return out
}

// tsOrZero converts an optional proto timestamp to a Go time, returning
// the zero value when unset (so the store treats it as "no bound").
func tsOrZero(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

// pageEcho normalises the request paging back for the response echo.
func pageEcho(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize
}
