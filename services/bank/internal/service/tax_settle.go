package service

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/jackc/pgx/v5"
)

// SettleCapitalGainsTaxInput is the payload for the spec p.62 tax debit.
// `AmountRSD` is the RSD-denominated tax owed; bank inverts the
// menjačnica when the user's account is in a different currency so
// exactly that much RSD lands on the state account.
type SettleCapitalGainsTaxInput struct {
	AccountID string
	AmountRSD string
	OpID      string
	Purpose   string
}

// SettleCapitalGainsTax debits the user's trading account by an amount
// equivalent to AmountRSD (in account.currency) and credits the state's
// RSD tax account. Spec p.62 explicitly waives commission on this
// conversion regardless of actor type, so we forward an actuary
// principal to executeMoneyMove which short-circuits commissionRateFor
// to zero.
func (s *Service) SettleCapitalGainsTax(ctx context.Context, in SettleCapitalGainsTaxInput) (*domain.PaymentResult, error) {
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
	rsdAmt, err := parsePositive(in.AmountRSD)
	if err != nil {
		return nil, err
	}

	from, err := s.Store.GetAccountByID(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	state, err := s.Store.GetStateTaxAccount(ctx)
	if err != nil {
		return nil, err
	}

	// Determine source-currency amount. When account is RSD this is a
	// straight debit; otherwise we ask the menjačnica engine "how much
	// of from.Currency converts to AmountRSD?". rateAndConvert with
	// (RSD → from.Currency, rsdAmt) returns exactly that figure since
	// it inverts the same ASK rate the second leg will use. Per spec
	// p.62 the conversion is commission-free, so the ASK rate cancels
	// across the two legs and the state account receives exactly
	// AmountRSD.
	fromAmt := rsdAmt
	if from.Currency != domain.CurrencyRSD {
		_, conv, err := s.rateAndConvert(ctx, domain.CurrencyRSD, from.Currency, rsdAmt)
		if err != nil {
			return nil, err
		}
		fromAmt = conv
	}

	// Forward actuary-flavored principal so the FX leg's commission
	// zeroes out (spec p.62).  Drop the caller's UserID so the
	// sentinel-admin doesn't land in transactions.initiator_client_id.
	// When trading forwards an origin principal alongside the sentinel
	// (the real taxpayer — see [[reference_be16_sentinel_origin_forwarding]])
	// and that origin is a client, stamp the client there so the tax
	// debit shows up on the user's own statement.
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
		purpose = "Porez na kapitalni dobitak"
	}

	return s.idempotentSettle(ctx, in.OpID, func(tx pgx.Tx) ([]*domain.Transaction, error) {
		return s.executeMoneyMove(ctx, tx, from, state, fromAmt, domain.TxKindTax, in.OpID, initiator, paymentMeta{Purpose: purpose}, 0)
	})
}
