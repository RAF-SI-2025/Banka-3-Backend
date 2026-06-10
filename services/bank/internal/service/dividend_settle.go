package service

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/jackc/pgx/v5"
)

// SettleDividendInput is the payload for the quarterly dividend payout
// (spec todoSpec C3 S54-S59). The trading service computes the gross
// per-holding dividend (shares × price × yield/4) in the security's
// listing currency and names the destination account; bank credits that
// account from its own per-currency house account. When the destination
// account is in a different currency the menjačnica engine converts —
// commission-free (a dividend is not a client-initiated FX trade), so
// an RSD-only holder still lands exactly the converted amount (S56).
type SettleDividendInput struct {
	AccountID string
	Amount    string
	Currency  domain.Currency
	OpID      string
	Purpose   string
}

// SettleDividend credits AccountID by an amount equal to Amount (in
// Currency, the security's listing currency) sourced from the bank's
// per-currency house account. Internal-only: the trading dividend cron
// authenticates with admin metadata. Idempotent on OpID — a retry after
// a partial cron failure surfaces the existing legs without re-crediting.
//
// The bank is the conceptual payer of the dividend; routing it from the
// per-currency house account keeps the money flow inside bank-owned
// accounts. For an actuary holding "in the name of the bank" (S58) the
// trading service names a bank-owned destination account, so the credit
// stays within the bank (Profit Banke) — the money never leaves.
func (s *Service) SettleDividend(ctx context.Context, in SettleDividendInput) (*domain.PaymentResult, error) {
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
	if !in.Currency.Supported() {
		return nil, apperr.Validation("unsupported currency")
	}
	amt, err := parsePositive(in.Amount)
	if err != nil {
		return nil, err
	}

	to, err := s.Store.GetAccountByID(ctx, in.AccountID)
	if err != nil {
		if apperrIs(err, apperr.KindNotFound) {
			s.log().WarnContext(ctx, "settle dividend: account not found",
				"err", err, "account_id", in.AccountID, "op_id", in.OpID)
		} else {
			s.log().ErrorContext(ctx, "settle dividend: account lookup failed",
				"err", err, "account_id", in.AccountID, "op_id", in.OpID)
		}
		return nil, err
	}
	house, err := s.Store.GetSystemAccount(ctx, in.Currency)
	if err != nil {
		s.log().ErrorContext(ctx, "settle dividend: house account lookup failed",
			"err", err, "currency", in.Currency, "op_id", in.OpID)
		return nil, err
	}

	// Crediting the bank's own house account from itself collapses to a
	// no-op pair; reject so we don't write zero-net ledger rows. (The
	// trading service routes Profit-Banke dividends to the forex_book,
	// not the menjačnica house, so this only catches a misrouted call.)
	if to.ID == house.ID {
		return nil, apperr.Validation("dividend cilj ne sme biti menjačnica banke")
	}

	// Forward an actuary-flavored principal so the cross-currency leg's
	// commission zeroes out — a dividend conversion is the bank paying
	// out, not a client FX trade. Stamp the recipient as initiator when
	// the trading adapter forwarded a client origin so the credit shows
	// on the holder's own statement (same BE-16 pattern as the tax path).
	initiator := auth.Principal{
		UserID:      "",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin, permissions.Actuary},
	}
	if origin, ok := auth.OriginPrincipalFrom(ctx); ok && origin.UserKind == auth.KindClient {
		initiator.UserID = origin.UserID
	}

	purpose := in.Purpose
	if purpose == "" {
		purpose = "Isplata dividende"
	}

	res, err := s.idempotentSettle(ctx, in.OpID, func(tx pgx.Tx) ([]*domain.Transaction, error) {
		return s.executeMoneyMove(ctx, tx, house, to, amt, domain.TxKindDividend, in.OpID, initiator, paymentMeta{Purpose: purpose}, 0)
	})
	if err != nil {
		return nil, err
	}
	s.log().InfoContext(ctx, "dividend settled",
		"op_id", in.OpID, "account_id", in.AccountID, "amount", in.Amount,
		"currency", in.Currency, "legs", len(res.Transactions))
	return res, nil
}
