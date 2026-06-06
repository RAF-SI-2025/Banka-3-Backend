// Cross-bank payment — user-initiated cash payment to a partner bank.
// Celina 5, spec p.77+.
//
// Why this lives in trading rather than bank: the saga orchestrator,
// outbound interbank HTTP client (PartnerPayer), and dialect detection
// all live here. Bank's gRPC-only service can't open HTTP connections
// to peer banks. Pure payments lives in bank; cross-bank payments
// reuse trading's saga rails to drive both sides of the 2PC.
//
// Request flow:
//   1. Auth: client principal with `payment.write` (same gate as
//      intra-bank payments). Verification dialog gates the route at
//      the gateway middleware layer; we don't re-check here.
//   2. Resolve the user's source account (UUID → 18-digit number,
//      currency, available_balance) via bank's AccountAvailable +
//      AccountNumber primitives.
//   3. Validate dest bank code + dest account number shape; reject
//      currency mismatch / insufficient available / unknown partner.
//   4. Derive saga transaction_id from (user_id, idempotency_key,
//      source_account_id) so client retries with the same idempotency
//      key never double-charge.
//   5. saga.Start runs the four-step flow synchronously to completion
//      (parking on transient errors).

package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
)

// SubmitCrossBankPaymentInput is the user-facing request shape.
type SubmitCrossBankPaymentInput struct {
	// IdempotencyKey — client-provided, kept stable across retries.
	// Combined with (user_id, source_account_id) to derive the saga
	// transaction_id; same key + payload → same saga row, no double
	// charge.
	IdempotencyKey string

	SourceAccountID     string // UUID — the user's local account
	RemoteBankCode      string // partner routing, e.g. "222"
	RemoteAccountNumber string // 18 digits, partner-prefixed

	Currency domain.Currency
	Amount   string
	Purpose  string
}

// SubmitCrossBankPaymentResult is what the FE polls back on.
type SubmitCrossBankPaymentResult struct {
	TransactionID string // saga + bank.interbank_protocol_transactions id
	Status        string // saga status — completed | running | failed
	LastError     string
}

// SubmitCrossBankPayment kicks off the cross-bank cash payment saga.
// Synchronous to saga completion (or parking on a transient bank-side
// error); the user sees the final state in the response.
func (s *Service) SubmitCrossBankPayment(ctx context.Context, in SubmitCrossBankPaymentInput) (*SubmitCrossBankPaymentResult, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.HasAny(p.Permissions, permissions.PaymentWrite, permissions.Admin) {
		return nil, apperr.PermissionDenied("nedovoljne permisije")
	}
	if s.SagaOrch == nil || s.InterbankPayer == nil || s.PartnerPayer == nil {
		return nil, apperr.FailedPrecondition("cross-bank payments nije konfigurisan")
	}
	if s.Reservations == nil {
		return nil, apperr.FailedPrecondition("bank rezervacije nisu povezane")
	}
	if in.IdempotencyKey == "" {
		return nil, apperr.Validation("idempotency_key je obavezan")
	}
	if in.SourceAccountID == "" {
		return nil, apperr.Validation("source_account_id je obavezan")
	}
	if len(in.RemoteAccountNumber) != 18 {
		return nil, apperr.Validation("destinacijski račun mora imati 18 cifara")
	}
	if in.RemoteBankCode == "" {
		return nil, apperr.Validation("destinacijska banka je obavezna")
	}
	// Spec p.77: each bank validates the account-number checksum on
	// every interbank tx. Same algorithm as our local accounts —
	// sum(all digits) % 11 == 0.
	if !accountNumberChecksumOK(in.RemoteAccountNumber) {
		return nil, apperr.Validation("checksum destinacijskog računa nije validan")
	}
	if !strings.HasPrefix(in.RemoteAccountNumber, in.RemoteBankCode) {
		return nil, apperr.Validation("destinacijski račun ne pripada navedenoj banci")
	}
	if !in.Currency.Supported() {
		return nil, apperr.Validation("nepodržana valuta")
	}
	amt, err := money.Parse(in.Amount)
	if err != nil || !money.IsPositive(amt) {
		return nil, apperr.Validation("iznos mora biti pozitivan")
	}
	_ = amt

	// Resolve source account: currency match + active.
	srcCcy, _, err := s.Reservations.AccountAvailable(ctx, in.SourceAccountID)
	if err != nil {
		return nil, err
	}
	if srcCcy != in.Currency {
		return nil, apperr.Validation("valuta računa se ne poklapa")
	}
	srcNumber, err := s.Reservations.AccountNumber(ctx, in.SourceAccountID)
	if err != nil {
		return nil, err
	}

	payload := crossBankPaymentPayload{
		UserID:              p.UserID,
		UserKind:            string(p.UserKind),
		SourceAccountID:     in.SourceAccountID,
		SourceAccountNumber: srcNumber,
		RemoteBankCode:      in.RemoteBankCode,
		RemoteAccountNumber: in.RemoteAccountNumber,
		Currency:            string(in.Currency),
		Amount:              in.Amount,
		Purpose:             in.Purpose,
		SenderRoutingNumber: s.Cfg.OwnRoutingNumber,
	}

	txID := crossBankPaymentTxID(p.UserID, in.IdempotencyKey, in.SourceAccountID)
	row, err := saga.Start(ctx, s.SagaOrch, saga.StartInput[crossBankPaymentPayload]{
		TransactionID: txID,
		SagaType:      crossBankPaymentSagaType,
		InitialState:  payload,
		AttemptsMax:   8,
	})
	if err != nil {
		return nil, err
	}
	// Retry queue (todoSpec): a saga left parked (status=running) means a
	// transient failure — typically the partner bank being unavailable at
	// prepare_partner. Enqueue a retry entry so the 5s/30s worker drives
	// it to completion (or aborts + notifies the client after 30s). The
	// saga's own attempt budget would eventually fail the row but without
	// releasing the local reservation, so the retry queue owns that
	// give-up path. Idempotent on txID.
	if row.Status == saga.StatusRunning {
		s.enqueueInterbankRetry(ctx, txID, in.RemoteBankCode, p.UserID, domain.UserKind(p.UserKind))
	}
	return &SubmitCrossBankPaymentResult{
		TransactionID: txID,
		Status:        string(row.Status),
		LastError:     row.LastError,
	}, nil
}

// GetCrossBankPayment returns the saga row for a transaction_id. Auth:
// either the originator (matches saga payload.user_id) or admin.
func (s *Service) GetCrossBankPayment(ctx context.Context, txID string) (*CrossBankPaymentView, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if s.SagaStore == nil {
		return nil, apperr.FailedPrecondition("saga store nije povezan")
	}
	row, err := s.SagaStore.Get(ctx, txID)
	if err != nil {
		return nil, err
	}
	if row == nil || row.SagaType != crossBankPaymentSagaType {
		return nil, apperr.NotFound("transakcija nije pronađena")
	}
	var payload crossBankPaymentPayload
	if err := json.Unmarshal(row.State, &payload); err != nil {
		return nil, apperr.Internal("decode saga state", err)
	}
	if payload.UserID != p.UserID && !permissions.Has(p.Permissions, permissions.Admin) {
		return nil, apperr.PermissionDenied("nije tvoja transakcija")
	}
	return &CrossBankPaymentView{
		TransactionID:       row.TransactionID,
		Status:              string(row.Status),
		CurrentStep:         row.CurrentStep,
		Attempts:            row.Attempts,
		LastError:           row.LastError,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
		SourceAccountID:     payload.SourceAccountID,
		SourceAccountNumber: payload.SourceAccountNumber,
		RemoteBankCode:      payload.RemoteBankCode,
		RemoteAccountNumber: payload.RemoteAccountNumber,
		Currency:            payload.Currency,
		Amount:              payload.Amount,
		Purpose:             payload.Purpose,
	}, nil
}

// CrossBankPaymentView is the gRPC-side projection of a saga row.
type CrossBankPaymentView struct {
	TransactionID       string
	Status              string // running | completed | failed | compensating
	CurrentStep         string
	Attempts            int
	LastError           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	SourceAccountID     string
	SourceAccountNumber string
	RemoteBankCode      string
	RemoteAccountNumber string
	Currency            string
	Amount              string
	Purpose             string
}

// accountNumberChecksumOK validates the spec p.20 / p.77 account-number
// checksum: sum of all digits mod 11 must be 0.
func accountNumberChecksumOK(num string) bool {
	if len(num) != 18 {
		return false
	}
	sum := 0
	for _, ch := range num {
		if ch < '0' || ch > '9' {
			return false
		}
		sum += int(ch - '0')
	}
	return sum%11 == 0
}
