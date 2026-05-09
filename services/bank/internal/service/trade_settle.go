package service

import (
	"context"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/jackc/pgx/v5"
)

// SettleTradeInput is the validated payload of a single trading-fill
// settlement (spec p.55-56). The trading service computes commission
// itself; the amount here is the net to move.
type SettleTradeInput struct {
	AccountID string
	Direction string // "debit" → user→bank house; "credit" → bank house→user
	Currency  domain.Currency
	Amount    string
	OpID      string
	IsActuary bool
	Purpose   string
}

// SettleTrade is the trading-service settlement entry point. It is
// internal-only: callers authenticate with admin metadata. We branch
// the executeMoneyMove path: for a buy we move from the user account
// to the bank's house in `currency`; for a sell, the inverse.
//
// FX is supported transparently — when the user's account currency
// differs from `currency`, the existing menjačnica engine kicks in.
// Actuary trades zero out the FX commission per spec p.26.
func (s *Service) SettleTrade(ctx context.Context, in SettleTradeInput) (*domain.PaymentResult, error) {
	// Internal-only. The trading service forwards admin metadata; the
	// gateway never routes a public request to this RPC (no http
	// annotation in proto), but defence-in-depth doesn't hurt.
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.Has(p.Permissions, permissions.Admin) {
		return nil, apperr.PermissionDenied("internal-only RPC")
	}

	if in.AccountID == "" || in.OpID == "" {
		return nil, apperr.Validation("account_id and op_id are required")
	}
	dir := strings.ToLower(strings.TrimSpace(in.Direction))
	if dir != "debit" && dir != "credit" {
		return nil, apperr.Validation("direction must be 'debit' or 'credit'")
	}
	if !in.Currency.Supported() {
		return nil, apperr.Validation("unsupported currency")
	}
	amt, err := parsePositive(in.Amount)
	if err != nil {
		return nil, err
	}

	user, err := s.Store.GetAccountByID(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	house, err := s.Store.GetSystemAccount(ctx, in.Currency)
	if err != nil {
		return nil, err
	}

	// Idempotency: if a transaction with this op_id already exists,
	// return the existing legs. Trading service may retry on a flaky
	// connection without re-charging.
	if existing, err := s.Store.GetTransactionsByOpID(ctx, in.OpID); err == nil && len(existing) > 0 {
		return &domain.PaymentResult{OpID: in.OpID, Status: domain.TxStatusRealized, Transactions: existing}, nil
	}

	// Direction selects which side is debited.
	var from, to *domain.Account
	if dir == "debit" {
		from, to = user, house
	} else {
		from, to = house, user
	}

	// Forward an actuary-flavored principal so the executeMoneyMove
	// FX leg zeros commission when this is an actuary trade. We
	// explicitly drop the caller's UserID so transactions.initiator_client_id
	// stays NULL — the trading service's sentinel UUID isn't a real
	// klijent and we don't want it indexed there.
	_ = p
	initiator := auth.Principal{
		UserID:      "",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	}
	if in.IsActuary {
		initiator.Permissions = append(initiator.Permissions, permissions.Actuary)
	}

	purpose := in.Purpose
	if purpose == "" {
		purpose = "Trgovinska poravnava"
	}

	result := &domain.PaymentResult{OpID: in.OpID, Status: domain.TxStatusRealized}
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		legs, err := s.executeMoneyMove(ctx, tx, from, to, amt, domain.TxKindTrade, in.OpID, initiator, paymentMeta{Purpose: purpose})
		if err != nil {
			return err
		}
		result.Transactions = legs
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
