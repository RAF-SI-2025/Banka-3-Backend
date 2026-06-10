// Inter-bank observability & control — supervisor read/control surface
// (celina 5). Backs the "Međubankarske transakcije" portal: transaction
// + status listing, comms / audit-log viewer, and blacklist management.
//
// Unlike the 2PC RPCs (PreparePayment/CommitPayment/…) which are
// internal-only (requireInternal → admin), these are supervisor-facing:
// requireInterbankSupervisor admits admin OR actuary.supervisor. They are
// read-mostly; BlockBank/UnblockBank mutate the blacklist.

package service

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/store"
)

// requireInterbankSupervisor admits a supervisor (actuary.supervisor) or
// an admin. The inter-bank observability surface is supervisor-facing,
// not internal-only.
func (s *Service) requireInterbankSupervisor(ctx context.Context) error {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return err
	}
	if permissions.Has(p.Permissions, permissions.Admin) ||
		permissions.Has(p.Permissions, permissions.ActuarySupervisor) {
		return nil
	}
	return apperr.PermissionDenied("nedovoljne permisije")
}

// ListInterbankTransactionsInput scopes the status-tracking view.
type ListInterbankTransactionsInput struct {
	SenderRoutingNumber int
	Status              domain.InterbankTxStatus
	Direction           domain.InterbankPaymentDirection
	From                time.Time
	To                  time.Time
	Page                int
	PageSize            int
}

// ListInterbankTransactions returns the cross-bank 2PC transactions with
// their full status flow (pending → prepared → committed/rolled_back, or
// failed), filterable by partner / status / direction / date range.
func (s *Service) ListInterbankTransactions(ctx context.Context, in ListInterbankTransactionsInput) ([]*domain.InterbankProtocolTransaction, int64, error) {
	if err := s.requireInterbankSupervisor(ctx); err != nil {
		return nil, 0, err
	}
	limit, offset := pageBounds(in.Page, in.PageSize)
	return s.Store.ListInterbankTransactions(ctx, store.InterbankTxFilter{
		SenderRoutingNumber: in.SenderRoutingNumber,
		Status:              string(in.Status),
		Direction:           string(in.Direction),
		From:                in.From,
		To:                  in.To,
	}, limit, offset)
}

// GetInterbankTransaction returns one transaction by its identity tuple.
func (s *Service) GetInterbankTransaction(ctx context.Context, senderRouting int, txID string) (*domain.InterbankProtocolTransaction, error) {
	if err := s.requireInterbankSupervisor(ctx); err != nil {
		return nil, err
	}
	if senderRouting == 0 || txID == "" {
		return nil, apperr.Validation("sender_routing_number and transaction_id are required")
	}
	return s.Store.GetInterbankTx(ctx, nil, senderRouting, txID, false)
}

// ListInterbankAuditLogInput scopes the comms / audit-log view.
type ListInterbankAuditLogInput struct {
	SenderRoutingNumber int
	MessageType         domain.InterbankMessageType
	From                time.Time
	To                  time.Time
	Page                int
	PageSize            int
}

// ListInterbankAuditLog returns the inbound-message comms history (every
// recorded partner NEW_TX / COMMIT_TX / ROLLBACK_TX plus the response we
// returned), filterable by partner / message type / date range.
func (s *Service) ListInterbankAuditLog(ctx context.Context, in ListInterbankAuditLogInput) ([]*domain.InterbankProtocolMessage, int64, error) {
	if err := s.requireInterbankSupervisor(ctx); err != nil {
		return nil, 0, err
	}
	limit, offset := pageBounds(in.Page, in.PageSize)
	return s.Store.ListInterbankMessages(ctx, store.InterbankMsgFilter{
		SenderRoutingNumber: in.SenderRoutingNumber,
		MessageType:         string(in.MessageType),
		From:                in.From,
		To:                  in.To,
	}, limit, offset)
}

// ListInterbankBlacklist returns blacklist rows. activeOnly restricts to
// currently-blocked partners; false returns the full history.
func (s *Service) ListInterbankBlacklist(ctx context.Context, activeOnly bool) ([]*domain.InterbankBlacklistEntry, error) {
	if err := s.requireInterbankSupervisor(ctx); err != nil {
		return nil, err
	}
	return s.Store.ListBlacklist(ctx, activeOnly)
}

// BlockInterbankPartner manually blacklists a partner bank. blocked_by is
// stamped with the acting supervisor's user id.
func (s *Service) BlockInterbankPartner(ctx context.Context, senderRouting int, reason string) (*domain.InterbankBlacklistEntry, error) {
	if err := s.requireInterbankSupervisor(ctx); err != nil {
		return nil, err
	}
	if senderRouting <= 0 {
		return nil, apperr.Validation("routing number is required")
	}
	by := ""
	if p, ok := auth.PrincipalFrom(ctx); ok {
		by = p.UserID
	}
	out, err := s.Store.BlockBank(ctx, senderRouting, reason, by)
	if err != nil {
		s.log().ErrorContext(ctx, "interbank block partner failed",
			"err", err, "sender_routing_number", senderRouting, "blocked_by", by)
		return nil, err
	}
	s.log().InfoContext(ctx, "interbank partner blocked",
		"sender_routing_number", senderRouting, "blocked_by", by, "reason", reason)
	return out, nil
}

// UnblockInterbankPartner lifts an active blacklist entry. The
// failure-counter reset lets the partner accrue a fresh streak rather
// than tripping the threshold on its next slip.
func (s *Service) UnblockInterbankPartner(ctx context.Context, senderRouting int) (*domain.InterbankBlacklistEntry, error) {
	if err := s.requireInterbankSupervisor(ctx); err != nil {
		return nil, err
	}
	if senderRouting <= 0 {
		return nil, apperr.Validation("routing number is required")
	}
	out, err := s.Store.UnblockBank(ctx, senderRouting)
	if err != nil {
		if clientClassErr(err) {
			s.log().WarnContext(ctx, "interbank unblock partner rejected",
				"err", err, "sender_routing_number", senderRouting)
		} else {
			s.log().ErrorContext(ctx, "interbank unblock partner failed",
				"err", err, "sender_routing_number", senderRouting)
		}
		return nil, err
	}
	if rerr := s.Store.ResetPartnerFailures(ctx, senderRouting); rerr != nil {
		s.log().WarnContext(ctx, "interbank: reset failures on unblock", "err", rerr, "sender_routing_number", senderRouting)
	}
	s.log().InfoContext(ctx, "interbank partner unblocked", "sender_routing_number", senderRouting)
	return out, nil
}

// pageBounds normalises 1-based page / page-size into limit + offset.
// Defaults: page 1, size 50; size capped at 200.
func pageBounds(page, pageSize int) (limit, offset int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return pageSize, (page - 1) * pageSize
}
