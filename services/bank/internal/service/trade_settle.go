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
	// Spec p.56: zaposleni (aktuari) trguju sa bankinog računa. Refuse
	// when an actuary-flagged settle targets a non-bank account.
	// Both KindSystem (menjačnica house) and KindForexBook (bank's
	// per-currency trading book) qualify as bank-owned. Picking the
	// menjačnica house collapses to a no-op against itself, so for
	// actuary trades we steer toward the forex_book.
	if in.IsActuary && user.Kind != domain.KindSystem && user.Kind != domain.KindForexBook {
		return nil, apperr.FailedPrecondition("aktuari mogu trgovati samo sa bankinog računa")
	}
	house, err := s.Store.GetSystemAccount(ctx, in.Currency)
	if err != nil {
		return nil, err
	}
	// Same-account trade (actuary debiting a bank account and crediting
	// it back) collapses to a no-op; reject so we don't write zero-net
	// transaction pairs against the menjačnica house.
	if in.IsActuary && user.ID == house.ID {
		return nil, apperr.Validation("aktuari moraju izabrati trading-book račun, ne menjačnicu")
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

// SettleForexFillInput pairs the two cash legs of a forex pair fill
// (spec p.42). Direction "buy" means the actuary buys the base
// currency by paying the quote currency; "sell" reverses both legs.
type SettleForexFillInput struct {
	Direction    string // "buy" | "sell" of the base currency
	BaseCurrency domain.Currency
	BaseAmount   string // qty × contract_size, in base currency
	QuoteCurrency domain.Currency
	QuoteAmount  string // qty × contract_size × price, in quote currency
	OpID         string
	Purpose      string
}

// SettleForexFill atomically moves the two paired legs of a forex fill
// between the bank's per-currency forex_book accounts (the "market"
// counterparty) and the per-currency menjačnica house. We use the
// existing executeMoneyMove engine for each leg with TxKindForex so
// the legs are auditable as forex_fill rows in the ledger.
//
// On a buy: house[quote] → forex_book[quote] (bank pays quote currency
// to the market) and forex_book[base] → house[base] (bank receives
// base currency). Sell reverses both flows.
//
// Idempotent on op_id: a retry that finds existing legs returns them
// unchanged.
func (s *Service) SettleForexFill(ctx context.Context, in SettleForexFillInput) (*domain.PaymentResult, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.Has(p.Permissions, permissions.Admin) {
		return nil, apperr.PermissionDenied("internal-only RPC")
	}
	_ = p

	if in.OpID == "" {
		return nil, apperr.Validation("op_id is required")
	}
	if !in.BaseCurrency.Supported() || !in.QuoteCurrency.Supported() {
		return nil, apperr.Validation("unsupported currency in pair")
	}
	if in.BaseCurrency == in.QuoteCurrency {
		return nil, apperr.Validation("forex pair currencies must differ")
	}
	dir := strings.ToLower(strings.TrimSpace(in.Direction))
	if dir != "buy" && dir != "sell" {
		return nil, apperr.Validation("direction must be 'buy' or 'sell'")
	}
	baseAmt, err := parsePositive(in.BaseAmount)
	if err != nil {
		return nil, apperr.Validation("base_amount: " + err.Error())
	}
	quoteAmt, err := parsePositive(in.QuoteAmount)
	if err != nil {
		return nil, apperr.Validation("quote_amount: " + err.Error())
	}

	if existing, err := s.Store.GetTransactionsByOpID(ctx, in.OpID); err == nil && len(existing) > 0 {
		return &domain.PaymentResult{OpID: in.OpID, Status: domain.TxStatusRealized, Transactions: existing}, nil
	}

	houseBase, err := s.Store.GetSystemAccount(ctx, in.BaseCurrency)
	if err != nil {
		return nil, err
	}
	houseQuote, err := s.Store.GetSystemAccount(ctx, in.QuoteCurrency)
	if err != nil {
		return nil, err
	}
	bookBase, err := s.Store.GetForexBookAccount(ctx, in.BaseCurrency)
	if err != nil {
		return nil, err
	}
	bookQuote, err := s.Store.GetForexBookAccount(ctx, in.QuoteCurrency)
	if err != nil {
		return nil, err
	}

	// Actuary-flavored initiator → executeMoneyMove zeros FX commission
	// (this won't matter since each leg is same-currency, but stays
	// consistent with SettleTrade's pattern).
	initiator := auth.Principal{
		UserID:      "",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin, permissions.Actuary},
	}
	purpose := in.Purpose
	if purpose == "" {
		purpose = "Forex fill"
	}

	// Direction wiring:
	//   buy  base: house[quote] → book[quote]   (bank pays quote to market)
	//             book[base]    → house[base]   (bank receives base from market)
	//   sell base: book[quote]  → house[quote]  (bank receives quote)
	//             house[base]   → book[base]    (bank pays base)
	var fromQuote, toQuote, fromBase, toBase *domain.Account
	if dir == "buy" {
		fromQuote, toQuote = houseQuote, bookQuote
		fromBase, toBase = bookBase, houseBase
	} else {
		fromQuote, toQuote = bookQuote, houseQuote
		fromBase, toBase = houseBase, bookBase
	}

	result := &domain.PaymentResult{OpID: in.OpID, Status: domain.TxStatusRealized}
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		quoteLegs, err := s.executeMoneyMove(ctx, tx, fromQuote, toQuote, quoteAmt, domain.TxKindForex, in.OpID, initiator, paymentMeta{Purpose: purpose})
		if err != nil {
			return err
		}
		baseLegs, err := s.executeMoneyMove(ctx, tx, fromBase, toBase, baseAmt, domain.TxKindForex, in.OpID, initiator, paymentMeta{Purpose: purpose})
		if err != nil {
			return err
		}
		result.Transactions = append(quoteLegs, baseLegs...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
