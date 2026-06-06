// Bank reservation primitive (c4, spec p.64-76). The SAGA orchestrator
// in trading uses these to settle multi-step intra-bank operations:
// OTC premium transfer, OTC contract exercise, fund invest, fund
// withdraw. The pattern is always the same — reserve on the source
// account (debits available_balance), do other SAGA steps, then either
// commit (finalises the debit and credits the destination) or release
// (compensating step that un-reserves).
//
// Idempotency: every RPC keys off the SAGA's deterministic op_id
// (uuid.NewSHA1(transaction_id, step_name)). A retry returns the
// existing reservation rather than double-debiting. The unique index
// on reservations.op_id is the backstop.

package service

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/store"
)

// allowedReservationOpKinds gates which op_kinds may flow through the
// reservation primitive. The set matches transactions.op_kind values
// added by migration 0012; any other tag is rejected at validation
// time so a typo can't insert a row with an unsupported kind.
var allowedReservationOpKinds = map[string]domain.TransactionKind{
	string(domain.TxKindOTCPremium):       domain.TxKindOTCPremium,
	string(domain.TxKindOTCExercise):      domain.TxKindOTCExercise,
	string(domain.TxKindFundInvest):       domain.TxKindFundInvest,
	string(domain.TxKindFundWithdraw):     domain.TxKindFundWithdraw,
	string(domain.TxKindInterbankPayment): domain.TxKindInterbankPayment,
	string(domain.TxKindForexForward):     domain.TxKindForexForward,
}

// ReserveFundsInput is the validated payload of an RPC call.
type ReserveFundsInput struct {
	AccountID string
	Amount    string // in account's currency
	Currency  domain.Currency
	OpID      string
	OpKind    string
}

// ReserveFundsResult is the post-state surfaced to the caller.
type ReserveFundsResult struct {
	ReservationID string
	OpID          string
}

// ReserveFunds debits available_balance and inserts a 'held'
// reservation row. Idempotent on op_id — a retry returns the existing
// reservation's id without touching the balance.
//
// The reservation amount is rejected when it exceeds available_balance
// (FailedPrecondition); the database's `available_balance >= 0`
// invariant via the AdjustBalance helper is the backstop.
func (s *Service) ReserveFunds(ctx context.Context, in ReserveFundsInput) (*ReserveFundsResult, error) {
	if err := s.requireInternal(ctx); err != nil {
		return nil, err
	}
	if in.AccountID == "" || in.OpID == "" {
		return nil, apperr.Validation("account_id and op_id are required")
	}
	if _, ok := allowedReservationOpKinds[in.OpKind]; !ok {
		return nil, apperr.Validation("unsupported op_kind")
	}
	if !in.Currency.Supported() {
		return nil, apperr.Validation("unsupported currency")
	}
	amt, err := parsePositive(in.Amount)
	if err != nil {
		return nil, err
	}

	// Idempotent fast-path: existing reservation → return it. Skip the
	// write tx and the balance debit.
	if existing, err := s.Store.GetReservationByOpID(ctx, in.OpID); err == nil && existing != nil {
		return &ReserveFundsResult{ReservationID: existing.ID, OpID: existing.OpID}, nil
	}

	acc, err := s.Store.GetAccountByID(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	if acc.Currency != in.Currency {
		return nil, apperr.Validation("reservation currency must match account")
	}
	if acc.Status != domain.AccountActive {
		return nil, apperr.FailedPrecondition("account not active")
	}

	negAmt := money.FormatAmount(money.Sub(money.MustParse("0"), amt))

	var inserted *domain.Reservation
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		// Debit available_balance only — leave balance alone. The
		// existing AdjustBalance helper updates both columns in lockstep,
		// so we use the dedicated AdjustAvailableBalance helper instead.
		if err := s.Store.AdjustAvailableBalance(ctx, tx, in.AccountID, negAmt); err != nil {
			return err
		}
		row, ierr := s.Store.InsertReservation(ctx, tx, &domain.Reservation{
			AccountID: in.AccountID,
			OpID:      in.OpID,
			Amount:    money.FormatAmount(amt),
			Currency:  in.Currency,
			OpKind:    in.OpKind,
		})
		if ierr != nil {
			return ierr
		}
		inserted = row
		return nil
	})
	if err != nil {
		// Race: a concurrent retry won the unique-constraint. Re-read
		// the winner's row and return it.
		if errors.Is(err, store.ErrReservationExists) || store.IsUniqueViolation(err) {
			winner, lerr := s.Store.GetReservationByOpID(ctx, in.OpID)
			if lerr == nil && winner != nil {
				return &ReserveFundsResult{ReservationID: winner.ID, OpID: winner.OpID}, nil
			}
		}
		return nil, err
	}
	return &ReserveFundsResult{ReservationID: inserted.ID, OpID: inserted.OpID}, nil
}

// ReleaseFundsResult signals whether this call moved the reservation
// from held→released or was a no-op (already released / never existed).
type ReleaseFundsResult struct {
	Released bool
}

// ReleaseFunds re-credits available_balance and flips the reservation
// row to 'released'. Idempotent: when called on an already-released
// reservation it returns Released=false. When the reservation doesn't
// exist (caller compensating before the forward step ever wrote the
// row) it also returns Released=false rather than NotFound — SAGA
// compensations must be tolerant of out-of-order execution.
func (s *Service) ReleaseFunds(ctx context.Context, opID string) (*ReleaseFundsResult, error) {
	if err := s.requireInternal(ctx); err != nil {
		return nil, err
	}
	if opID == "" {
		return nil, apperr.Validation("op_id is required")
	}

	var released bool
	err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		row, err := s.Store.GetReservationByOpIDTx(ctx, tx, opID)
		if err != nil {
			if apperrIs(err, apperr.KindNotFound) {
				return nil
			}
			return err
		}
		if row.State != domain.ReservationHeld {
			return nil
		}
		// Credit back the available_balance we debited at reserve time.
		// Use AdjustAvailableBalance again so the `balance` column is
		// untouched (commit is the only path that moves real money).
		if err := s.Store.AdjustAvailableBalance(ctx, tx, row.AccountID, row.Amount); err != nil {
			return err
		}
		if err := s.Store.MarkReservationState(ctx, tx, opID, domain.ReservationReleased); err != nil {
			return err
		}
		released = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &ReleaseFundsResult{Released: released}, nil
}

// CommitReservedFundsInput finalises a held reservation. The reserved
// account is credited from the reservation row; the destination is
// credited by `dest_amount`. Same-currency commits write one ledger
// leg; cross-currency hops via the menjačnica engine like a normal
// payment.
type CommitReservedFundsInput struct {
	OpID          string
	DestAccountID string
	DestAmount    string
	DestCurrency  domain.Currency
	IsActuary     bool
	Purpose       string
}

// CommitReservedFunds drives the second half of a reservation:
//
//  1. Resolve the held row (FOR UPDATE so a concurrent release blocks).
//  2. Debit `balance` on the source (available_balance already debited
//     at reserve time, so balance catches up here).
//  3. Credit balance + available_balance on the destination.
//  4. Write the ledger leg (single leg same-currency; two legs FX via
//     the bank's house accounts using the existing menjačnica engine).
//  5. Flip the reservation row to 'committed'.
//
// Idempotent on op_id: a retry whose reservation already shows
// 'committed' returns the existing ledger legs unchanged.
func (s *Service) CommitReservedFunds(ctx context.Context, in CommitReservedFundsInput) (*domain.PaymentResult, error) {
	if err := s.requireInternal(ctx); err != nil {
		return nil, err
	}
	if in.OpID == "" || in.DestAccountID == "" {
		return nil, apperr.Validation("op_id and dest_account_id are required")
	}
	if !in.DestCurrency.Supported() {
		return nil, apperr.Validation("unsupported destination currency")
	}
	destAmt, err := parsePositive(in.DestAmount)
	if err != nil {
		return nil, err
	}

	// Idempotent fast-path: if a commit has already settled this op_id,
	// return its ledger legs unchanged.
	if legs, err := s.Store.GetTransactionsByOpID(ctx, in.OpID); err == nil && len(legs) > 0 {
		return &domain.PaymentResult{OpID: in.OpID, Status: domain.TxStatusRealized, Transactions: legs}, nil
	}

	var legs []*domain.Transaction
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		res, gerr := s.Store.GetReservationByOpIDTx(ctx, tx, in.OpID)
		if gerr != nil {
			return gerr
		}
		switch res.State {
		case domain.ReservationCommitted:
			// Already committed; the fast-path catch-up above should have
			// returned. If we got here the legs were missing — treat as
			// internal.
			return apperr.Internal("reservation committed but no ledger legs found", nil)
		case domain.ReservationReleased:
			return apperr.FailedPrecondition("reservation already released")
		case domain.ReservationHeld:
			// fall through
		default:
			return apperr.Internal("unknown reservation state", nil)
		}

		src, err := s.Store.GetAccountByID(ctx, res.AccountID)
		if err != nil {
			return err
		}
		dst, err := s.Store.GetAccountByID(ctx, in.DestAccountID)
		if err != nil {
			return err
		}
		if dst.Currency != in.DestCurrency {
			return apperr.Validation("dest_currency must match destination account")
		}
		if src.Status != domain.AccountActive || dst.Status != domain.AccountActive {
			return apperr.FailedPrecondition("jedan od računa nije aktivan")
		}

		srcAmt, perr := money.Parse(res.Amount)
		if perr != nil {
			return apperr.Internal("reservation amount unparseable", perr)
		}

		// Bring `balance` in line with available_balance (which was
		// already debited at reserve time). Note: we do NOT call
		// AdjustBalance — that path debits available_balance again. We
		// debit `balance` only, via a dedicated store helper.
		if err := s.Store.AdjustBalanceOnly(ctx, tx, src.ID, money.FormatAmount(money.Sub(money.MustParse("0"), srcAmt))); err != nil {
			return err
		}

		// Credit the destination's balance + available_balance.
		if err := s.Store.AdjustBalance(ctx, tx, dst.ID, money.FormatAmount(destAmt)); err != nil {
			return err
		}

		// Build ledger legs. Same currency → one leg src → dst at the
		// reservation amount (the caller pre-validates that
		// dest_amount == reservation amount in the same-currency path).
		// Different currencies → two legs hopping through the bank's
		// per-currency house accounts.
		kind := domain.TransactionKind(res.OpKind)
		purpose := in.Purpose
		if purpose == "" {
			purpose = strings.ToUpper(res.OpKind[:1]) + res.OpKind[1:]
		}
		initiator := auth.Principal{
			UserKind:    auth.KindEmployee,
			Permissions: []string{permissions.Admin},
		}
		if in.IsActuary {
			initiator.Permissions = append(initiator.Permissions, permissions.Actuary)
		}

		if src.Currency == dst.Currency {
			leg, lerr := s.Store.InsertTransaction(ctx, tx, &domain.Transaction{
				OpID:          in.OpID,
				Kind:          kind,
				LegIndex:      1,
				FromAccountID: src.ID,
				ToAccountID:   dst.ID,
				FromAmount:    money.FormatAmount(srcAmt),
				ToAmount:      money.FormatAmount(destAmt),
				Purpose:       purpose,
				Status:        domain.TxStatusRealized,
			})
			if lerr != nil {
				return lerr
			}
			legs = []*domain.Transaction{leg}
			return s.Store.MarkReservationState(ctx, tx, in.OpID, domain.ReservationCommitted)
		}

		// Cross-currency: hop through the bank's per-currency house
		// accounts. This mirrors executeMoneyMove's two-leg FX path,
		// but the source debit is already booked (balance only — the
		// reservation came off available_balance at reserve time, the
		// balance debit we just did closes the loop). So we just
		// write the FX-leg ledger rows for src → houseSrc and
		// houseDst → dst, with the commission deducted from the dst
		// side when this is a client (IsActuary=false).
		bankFrom, err := s.Store.GetSystemAccount(ctx, src.Currency)
		if err != nil {
			return err
		}
		bankTo, err := s.Store.GetSystemAccount(ctx, dst.Currency)
		if err != nil {
			return err
		}
		// Move the reservation's currency from houseSrc → src (net-zero
		// effect: src.balance was debited above; here we adjust the
		// house). Actually: src already debited; what we need is to add
		// srcAmt to houseSrc (the bank receives the source currency)
		// and subtract destAmt from houseDst (the bank pays out the
		// destination currency). Then the leg rows describe both moves.
		if err := s.Store.AdjustBalance(ctx, tx, bankFrom.ID, money.FormatAmount(srcAmt)); err != nil {
			return err
		}
		if err := s.Store.AdjustBalance(ctx, tx, bankTo.ID, money.FormatAmount(money.Sub(money.MustParse("0"), destAmt))); err != nil {
			return err
		}

		leg1, lerr := s.Store.InsertTransaction(ctx, tx, &domain.Transaction{
			OpID:          in.OpID,
			Kind:          kind,
			LegIndex:      1,
			FromAccountID: src.ID,
			ToAccountID:   bankFrom.ID,
			FromAmount:    money.FormatAmount(srcAmt),
			ToAmount:      money.FormatAmount(srcAmt),
			Purpose:       purpose,
			Status:        domain.TxStatusRealized,
		})
		if lerr != nil {
			return lerr
		}
		leg2, lerr := s.Store.InsertTransaction(ctx, tx, &domain.Transaction{
			OpID:          in.OpID,
			Kind:          kind,
			LegIndex:      2,
			FromAccountID: bankTo.ID,
			ToAccountID:   dst.ID,
			FromAmount:    money.FormatAmount(destAmt),
			ToAmount:      money.FormatAmount(destAmt),
			Purpose:       purpose,
			Status:        domain.TxStatusRealized,
		})
		if lerr != nil {
			return lerr
		}
		legs = []*domain.Transaction{leg1, leg2}
		return s.Store.MarkReservationState(ctx, tx, in.OpID, domain.ReservationCommitted)
	})
	if err != nil {
		return nil, err
	}
	return &domain.PaymentResult{OpID: in.OpID, Status: domain.TxStatusRealized, Transactions: legs}, nil
}

// TransferBetweenClientsInput is the convenience wrapper input.
type TransferBetweenClientsInput struct {
	FromAccountID string
	ToAccountID   string
	Amount        string
	OpID          string
	OpKind        string
	IsActuary     bool
	Purpose       string
}

// TransferBetweenClients composes ReserveFunds + CommitReservedFunds in
// one shot. Used by OTC and fund flows when they want a one-call money
// move under their SAGA's op_id. Same-currency same-amount commit; FX
// hop is supported transparently.
func (s *Service) TransferBetweenClients(ctx context.Context, in TransferBetweenClientsInput) (*domain.PaymentResult, error) {
	if err := s.requireInternal(ctx); err != nil {
		return nil, err
	}
	if in.FromAccountID == "" || in.ToAccountID == "" || in.OpID == "" {
		return nil, apperr.Validation("from, to, op_id are required")
	}
	if _, ok := allowedReservationOpKinds[in.OpKind]; !ok {
		return nil, apperr.Validation("unsupported op_kind")
	}
	amt, err := parsePositive(in.Amount)
	if err != nil {
		return nil, err
	}

	// Idempotent fast-path: legs already exist for this op_id.
	if legs, lerr := s.Store.GetTransactionsByOpID(ctx, in.OpID); lerr == nil && len(legs) > 0 {
		return &domain.PaymentResult{OpID: in.OpID, Status: domain.TxStatusRealized, Transactions: legs}, nil
	}

	from, err := s.Store.GetAccountByID(ctx, in.FromAccountID)
	if err != nil {
		return nil, err
	}
	// Reserve in source currency; commit produces dest currency amount
	// (commission applied for clients when FX hop is needed — same
	// menjačnica policy as CreatePayment).
	if _, err := s.ReserveFunds(ctx, ReserveFundsInput{
		AccountID: in.FromAccountID,
		Amount:    money.FormatAmount(amt),
		Currency:  from.Currency,
		OpID:      in.OpID,
		OpKind:    in.OpKind,
	}); err != nil {
		return nil, err
	}

	to, err := s.Store.GetAccountByID(ctx, in.ToAccountID)
	if err != nil {
		// Best-effort release so we don't leak a held reservation.
		_, _ = s.ReleaseFunds(ctx, in.OpID)
		return nil, err
	}

	// Same-currency: dest_amount = source amount.
	if from.Currency == to.Currency {
		return s.CommitReservedFunds(ctx, CommitReservedFundsInput{
			OpID:          in.OpID,
			DestAccountID: in.ToAccountID,
			DestAmount:    money.FormatAmount(amt),
			DestCurrency:  to.Currency,
			IsActuary:     in.IsActuary,
			Purpose:       in.Purpose,
		})
	}

	// FX hop: compute the converted amount via the menjačnica engine
	// (ASK on every leg per spec p.26), subtract commission for client
	// transfers. Use the same rateAndConvert / commissionRateFor helpers
	// CreatePayment uses so the two paths agree byte-for-byte.
	initiator := auth.Principal{Permissions: []string{permissions.Admin}}
	if in.IsActuary {
		initiator.Permissions = append(initiator.Permissions, permissions.Actuary)
	}
	_, toBefore, err := s.rateAndConvert(ctx, from.Currency, to.Currency, amt)
	if err != nil {
		_, _ = s.ReleaseFunds(ctx, in.OpID)
		return nil, err
	}
	commission := money.Mul(toBefore, s.commissionRateFor(initiator))
	toAmt := money.Sub(toBefore, commission)
	if !money.IsPositive(toAmt) {
		_, _ = s.ReleaseFunds(ctx, in.OpID)
		return nil, apperr.Validation("amount too small after commission")
	}
	return s.CommitReservedFunds(ctx, CommitReservedFundsInput{
		OpID:          in.OpID,
		DestAccountID: in.ToAccountID,
		DestAmount:    money.FormatAmount(toAmt),
		DestCurrency:  to.Currency,
		IsActuary:     in.IsActuary,
		Purpose:       in.Purpose,
	})
}

// requireInternal admits only admin principals. The four reservation
// RPCs are internal-only (no http annotation in proto) so the gateway
// never routes a client request to them; defence-in-depth.
func (s *Service) requireInternal(ctx context.Context) error {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return err
	}
	if !permissions.Has(p.Permissions, permissions.Admin) {
		return apperr.PermissionDenied("internal-only RPC")
	}
	return nil
}

// apperrIs reports whether err is an apperr with the given kind. Tiny
// helper to keep the reservations service's no-op branches tight.
func apperrIs(err error, kind apperr.Kind) bool {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Kind == kind
}
