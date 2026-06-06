package service

import (
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/bizmetric"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreatePaymentInput is the validated payload for a Klijent → other-
// Klijent payment.
type CreatePaymentInput struct {
	FromAccountID   string
	ToAccountNumber string
	Amount          string // in from-account currency
	RecipientName   string
	PaymentCode     string
	ReferenceNumber string
	Purpose         string
	SaveRecipient   bool
}

// CreatePayment implements spec p.21 "Novo plaćanje". Same currency
// produces a single ledger leg; FX produces two legs through the
// bank's house accounts (menjačnica).
func (s *Service) CreatePayment(ctx context.Context, in CreatePaymentInput) (res *domain.PaymentResult, err error) {
	// banka_payments_total emit + structured log on every return.
	var currency string
	defer func() {
		c := currency
		if c == "" {
			c = "unknown"
		}
		bizmetric.PaymentCompleted(ctx, "payment", c, paymentStatus(err))
		if err != nil {
			s.Log.WarnContext(ctx, "CreatePayment failed",
				"from_account", in.FromAccountID,
				"to_account_no", in.ToAccountNumber,
				"amount", in.Amount,
				"currency", c,
				"err", err.Error())
		}
	}()

	if err := s.requirePermission(ctx, permissions.PaymentWrite); err != nil {
		return nil, err
	}
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}

	from, to, amt, err := s.resolvePaymentEndpoints(ctx, in.FromAccountID, in.ToAccountNumber, in.Amount, p)
	if err != nil {
		return nil, err
	}
	currency = string(from.Currency)
	if from.OwnerClientID == to.OwnerClientID {
		return nil, apperr.Validation("plaćanje je između različitih klijenata; za prebacivanje u okviru istog klijenta koristite Prenos")
	}

	code := strings.TrimSpace(in.PaymentCode)
	if code == "" {
		code = "289" // spec p.21 default
	}

	op := uuid.NewString()
	result := &domain.PaymentResult{OpID: op, Status: domain.TxStatusRealized}

	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		legs, err := s.executeMoneyMove(ctx, tx, from, to, amt, domain.TxKindPayment, op, p, paymentMeta{
			RecipientName:   in.RecipientName,
			PaymentCode:     code,
			ReferenceNumber: in.ReferenceNumber,
			Purpose:         in.Purpose,
		}, 0)
		if err != nil {
			return err
		}
		result.Transactions = legs
		return nil
	})
	if err != nil {
		return nil, err
	}

	if in.SaveRecipient && p.UserKind == auth.KindClient {
		_, _ = s.Store.UpsertPaymentRecipient(ctx, &domain.PaymentRecipient{
			ClientID:      p.UserID,
			Name:          strings.TrimSpace(in.RecipientName),
			AccountNumber: to.Number,
		})
	}

	// Spec E2E "Klijent dobija email potvrdu" — sender side only.
	s.notifyPaymentSucceeded(ctx, from.OwnerClientID, from.Number, to.Number, money.FormatAmount(amt), from.Currency)

	return result, nil
}

// CreateTransferInput is the validated payload for a same-client
// own-account transfer (spec p.23 "Prenos").
type CreateTransferInput struct {
	FromAccountID string
	ToAccountID   string
	Amount        string
	Purpose       string
}

func (s *Service) CreateTransfer(ctx context.Context, in CreateTransferInput) (res *domain.PaymentResult, err error) {
	var currency, kindLabel string
	defer func() {
		c := currency
		if c == "" {
			c = "unknown"
		}
		k := kindLabel
		if k == "" {
			k = "transfer"
		}
		bizmetric.PaymentCompleted(ctx, k, c, paymentStatus(err))
		if err != nil {
			s.Log.WarnContext(ctx, "CreateTransfer failed",
				"from", in.FromAccountID, "to", in.ToAccountID,
				"amount", in.Amount, "currency", c, "err", err.Error())
		}
	}()

	if err := s.requirePermission(ctx, permissions.PaymentWrite); err != nil {
		return nil, err
	}
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if in.FromAccountID == "" || in.ToAccountID == "" {
		return nil, apperr.Validation("from and to are required")
	}
	if in.FromAccountID == in.ToAccountID {
		return nil, apperr.Validation("from and to must differ")
	}

	from, err := s.Store.GetAccountByID(ctx, in.FromAccountID)
	if err != nil {
		return nil, err
	}
	currency = string(from.Currency)
	to, err := s.Store.GetAccountByID(ctx, in.ToAccountID)
	if err != nil {
		return nil, err
	}
	if p.UserKind == auth.KindClient && (from.OwnerClientID != p.UserID || to.OwnerClientID != p.UserID) {
		return nil, apperr.PermissionDenied("nedovoljne permisije")
	}
	if from.OwnerClientID != to.OwnerClientID {
		return nil, apperr.Validation("transfer je između računa istog klijenta; za drugi račun koristite Plaćanje")
	}

	amt, err := parsePositive(in.Amount)
	if err != nil {
		return nil, err
	}

	op := uuid.NewString()
	result := &domain.PaymentResult{OpID: op, Status: domain.TxStatusRealized}

	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		// Same-currency transfer = direct; cross-currency = menjačnica
		// (TxKindExchange) so the FE filter ("Domaća plaćanja" vs
		// "Menjačnica" tab on Pregled plaćanja) can render correctly.
		kind := domain.TxKindTransfer
		kindLabel = "transfer"
		if from.Currency != to.Currency {
			kind = domain.TxKindExchange
			kindLabel = "exchange"
		}
		legs, err := s.executeMoneyMove(ctx, tx, from, to, amt, kind, op, p, paymentMeta{Purpose: in.Purpose}, 0)
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

// paymentStatus maps a CreatePayment / CreateTransfer return into the
// stable label set used by banka_payments_total's `status` attribute.
func paymentStatus(err error) string {
	if err == nil {
		return "ok"
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Kind.String()
	}
	return "internal"
}

// =====================================================================
// Internal — money movement
// =====================================================================

type paymentMeta struct {
	RecipientName   string
	PaymentCode     string
	ReferenceNumber string
	Purpose         string
}

// executeMoneyMove is the shared engine behind both CreatePayment and
// CreateTransfer. It debits source, credits destination, optionally
// hops through bank house accounts on FX, and writes 1-2 ledger legs.
//
// All balance updates run inside the caller's pgx.Tx so a partial
// failure rolls back fully. The principal carries through so the
// FX-commission branch can short-circuit for actuary trades.
//
// legOffset is added to each leg's stored leg_index so callers that
// chain multiple executeMoneyMove invocations under a single op_id
// (SettleForexFill) keep the (op_id, leg_index) unique-index invariant.
// All other callers pass 0.
func (s *Service) executeMoneyMove(ctx context.Context, tx pgx.Tx, from, to *domain.Account, fromAmt *big.Rat, kind domain.TransactionKind, opID string, initiator auth.Principal, meta paymentMeta, legOffset int) ([]*domain.Transaction, error) {
	if from.Status != domain.AccountActive || to.Status != domain.AccountActive {
		return nil, apperr.FailedPrecondition("jedan od računa nije aktivan")
	}

	if err := s.Store.CheckLimits(ctx, tx, from.ID, money.FormatAmount(fromAmt)); err != nil {
		return nil, err
	}

	// Same currency — single leg.
	if from.Currency == to.Currency {
		fromStr := money.FormatAmount(fromAmt)
		negFrom := money.FormatAmount(money.Sub(money.MustParse("0"), fromAmt))
		if err := s.Store.AdjustBalance(ctx, tx, from.ID, negFrom); err != nil {
			return nil, err
		}
		if err := s.Store.AdjustBalance(ctx, tx, to.ID, fromStr); err != nil {
			return nil, err
		}
		leg, err := s.Store.InsertTransaction(ctx, tx, &domain.Transaction{
			OpID: opID, Kind: kind, LegIndex: legOffset + 1,
			FromAccountID: from.ID, ToAccountID: to.ID,
			FromAmount: fromStr, ToAmount: fromStr,
			RecipientName: meta.RecipientName, PaymentCode: meta.PaymentCode,
			ReferenceNumber: meta.ReferenceNumber, Purpose: meta.Purpose,
			InitiatorClientID: initiator.UserID,
			Status:            domain.TxStatusRealized,
		})
		if err != nil {
			return nil, err
		}
		return []*domain.Transaction{leg}, nil
	}

	// Cross-currency — menjačnica via bank house accounts.
	bankFrom, err := s.Store.GetSystemAccount(ctx, from.Currency)
	if err != nil {
		return nil, err
	}
	bankTo, err := s.Store.GetSystemAccount(ctx, to.Currency)
	if err != nil {
		return nil, err
	}

	composite, toBefore, err := s.rateAndConvert(ctx, from.Currency, to.Currency, fromAmt)
	if err != nil {
		return nil, err
	}
	commission := money.Mul(toBefore, s.commissionRateFor(initiator))
	toAmt := money.Sub(toBefore, commission)
	if !money.IsPositive(toAmt) {
		return nil, apperr.Validation("amount too small after commission")
	}

	fromStr := money.FormatAmount(fromAmt)
	toStr := money.FormatAmount(toAmt)
	negFrom := money.FormatAmount(money.Sub(money.MustParse("0"), fromAmt))
	negTo := money.FormatAmount(money.Sub(money.MustParse("0"), toAmt))

	// Debit source, credit bank's source-currency house.
	if err := s.Store.AdjustBalance(ctx, tx, from.ID, negFrom); err != nil {
		return nil, err
	}
	if err := s.Store.AdjustBalance(ctx, tx, bankFrom.ID, fromStr); err != nil {
		return nil, err
	}
	// Debit bank's to-currency house, credit destination (commission
	// is the spread the bank keeps; bankTo loses toAmt only — not
	// toBefore — so the difference accrues to the bank's books).
	if err := s.Store.AdjustBalance(ctx, tx, bankTo.ID, negTo); err != nil {
		return nil, err
	}
	if err := s.Store.AdjustBalance(ctx, tx, to.ID, toStr); err != nil {
		return nil, err
	}

	leg1, err := s.Store.InsertTransaction(ctx, tx, &domain.Transaction{
		OpID: opID, Kind: kind, LegIndex: legOffset + 1,
		FromAccountID: from.ID, ToAccountID: bankFrom.ID,
		FromAmount: fromStr, ToAmount: fromStr,
		RecipientName: meta.RecipientName, PaymentCode: meta.PaymentCode,
		ReferenceNumber: meta.ReferenceNumber, Purpose: meta.Purpose,
		InitiatorClientID: initiator.UserID,
		Status:            domain.TxStatusRealized,
	})
	if err != nil {
		return nil, err
	}
	leg2, err := s.Store.InsertTransaction(ctx, tx, &domain.Transaction{
		OpID: opID, Kind: kind, LegIndex: legOffset + 2,
		FromAccountID: bankTo.ID, ToAccountID: to.ID,
		FromAmount: toStr, ToAmount: toStr,
		Rate:              money.FormatRate(composite),
		InitiatorClientID: initiator.UserID,
		Status:            domain.TxStatusRealized,
	})
	if err != nil {
		return nil, err
	}
	return []*domain.Transaction{leg1, leg2}, nil
}

// resolvePaymentEndpoints loads source + destination accounts, runs
// per-principal-kind ownership checks, and parses the amount.
func (s *Service) resolvePaymentEndpoints(ctx context.Context, fromID, toNumber, amount string, p auth.Principal) (*domain.Account, *domain.Account, *big.Rat, error) {
	if fromID == "" || toNumber == "" {
		return nil, nil, nil, apperr.Validation("from_account_id and to_account_number are required")
	}
	from, err := s.Store.GetAccountByID(ctx, fromID)
	if err != nil {
		return nil, nil, nil, err
	}
	to, err := s.Store.GetAccountByNumber(ctx, toNumber)
	if err != nil {
		return nil, nil, nil, err
	}
	if p.UserKind == auth.KindClient && from.OwnerClientID != p.UserID {
		return nil, nil, nil, apperr.PermissionDenied("nedovoljne permisije")
	}
	amt, err := parsePositive(amount)
	if err != nil {
		return nil, nil, nil, err
	}
	return from, to, amt, nil
}

func parsePositive(s string) (*big.Rat, error) {
	r, err := money.Parse(s)
	if err != nil {
		return nil, apperr.Validation(err.Error())
	}
	if !money.IsPositive(r) {
		return nil, apperr.Validation("amount must be positive")
	}
	return r, nil
}
