// Inter-bank 2-phase commit primitive (celina 5 — spec p.77+).
//
// Five RPCs:
//
//   * PreparePayment  — reserve the cash leg (outbound) or validate +
//     record (inbound). Idempotent on (sender_routing_number,
//     transaction_id).
//   * CommitPayment   — finalise the prepared row, write the ledger leg.
//   * RollbackPayment — release a prepared row.
//   * RecordInboundMessage / GetInboundMessage — gateway-side replay
//     cache for partner idempotence keys.
//
// Outbound layout (we are the sending bank):
//   prepare → bank.reservations row in 'held' state (source = user's
//   account, op_kind = 'interbank_payment'); op_id derived
//   deterministically from (sender, tx_id).
//   commit  → CommitReservedFunds with dest = bank.system house
//   account in the currency (the funds conceptually leave for the
//   partner bank; we credit our own system account as the local
//   counterpart).
//   rollback → ReleaseFunds.
//
// Inbound layout (we are the receiving bank):
//   prepare → just validate the destination account; no reservation
//   (the partner debited their user, we don't move money yet).
//   commit  → write one transactions leg crediting the dest from the
//   bank's system account.
//   rollback → status flip only.
//
// transaction_id is text on the wire (partner banks don't all use
// UUIDs) but bank.reservations.op_id and bank.transactions.op_id are
// uuid. The mapping is uuid.NewSHA1(c5Namespace, sender:tx_id) — same
// pattern used by the SAGA framework for step op_ids.

package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// c5Namespace is the uuid.NewSHA1 namespace for deriving deterministic
// op_ids from a (sender_routing_number, transaction_id) tuple. Random
// UUIDv4 — not security-sensitive, just needs to be stable.
var c5Namespace = uuid.MustParse("a0b8e21b-3f0a-4f7a-8a0e-9b8a37c54a14")

// deriveOpID returns a deterministic UUID for a partner's transaction.
// Stable across retries — the unique-index backstop on bank.reservations
// + bank.transactions then makes the whole flow idempotent.
func deriveOpID(senderRouting int, txID string) string {
	return uuid.NewSHA1(c5Namespace, []byte(fmt.Sprintf("%d:%s", senderRouting, txID))).String()
}

// PreparePaymentInput captures every field a partner can send on
// NEW_TX. Validation is at the service edge — checksum is enforced one
// layer up (gateway).
type PreparePaymentInput struct {
	SenderRoutingNumber int
	TransactionID       string
	Direction           domain.InterbankPaymentDirection
	LocalAccountNumber  string
	RemoteAccountNumber string
	Currency            domain.Currency
	Amount              string
	TransactionBody     string
	Purpose             string
}

// PreparePaymentResult is what the partner gets back. ReservationID is
// non-empty only for outbound legs.
type PreparePaymentResult struct {
	TransactionID string
	Status        domain.InterbankTxStatus
	ReservationID string
}

// PreparePayment locks the resources needed to commit a cross-bank
// payment. Idempotent on (sender_routing_number, transaction_id).
//
// A blacklisted partner is rejected up front. On any prepare failure the
// partner's consecutive-failure counter is bumped (auto-blocking it once
// it crosses the threshold) and a 'failed' audit row is recorded so the
// supervisor status view shows the attempt; a success resets the counter.
func (s *Service) PreparePayment(ctx context.Context, in PreparePaymentInput) (*PreparePaymentResult, error) {
	if err := s.requireInternal(ctx); err != nil {
		return nil, err
	}

	res, err := s.preparePayment(ctx, in)
	if err != nil {
		// Idempotent replays surfacing as a found existing row never reach
		// here. Record the failed attempt + bump the failure counter so
		// the partner can be auto-blocked. Best-effort — never mask the
		// original error.
		s.onPrepareFailure(ctx, in, err)
		return nil, err
	}
	// A clean prepare clears any prior failure streak.
	if rerr := s.Store.ResetPartnerFailures(ctx, in.SenderRoutingNumber); rerr != nil {
		s.Log.Warn("interbank: reset partner failures", "sender_routing_number", in.SenderRoutingNumber, "error", rerr)
	}
	return res, nil
}

func (s *Service) preparePayment(ctx context.Context, in PreparePaymentInput) (*PreparePaymentResult, error) {
	if in.TransactionID == "" || in.SenderRoutingNumber == 0 {
		return nil, apperr.Validation("transaction_id and sender_routing_number are required")
	}

	// Idempotent fast-path: an existing row (any status) wins so a retry
	// never re-charges and a previously-failed attempt isn't re-counted.
	if existing, err := s.Store.GetInterbankTx(ctx, nil, in.SenderRoutingNumber, in.TransactionID, false); err == nil {
		if existing.Status == domain.InterbankTxFailed {
			return nil, apperr.FailedPrecondition("transaction previously failed")
		}
		return &PreparePaymentResult{
			TransactionID: existing.TransactionID,
			Status:        existing.Status,
			ReservationID: existing.ReservationID,
		}, nil
	}

	// Blacklist gate — refuse any leg from a blocked partner.
	blocked, err := s.Store.IsBlacklisted(ctx, in.SenderRoutingNumber)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, apperr.FailedPrecondition("partner bank is blacklisted")
	}

	if len(in.LocalAccountNumber) != 18 || len(in.RemoteAccountNumber) != 18 {
		return nil, apperr.Validation("account numbers must be 18 digits")
	}
	if !in.Currency.Supported() {
		return nil, apperr.Validation("unsupported currency")
	}
	amt, err := parsePositive(in.Amount)
	if err != nil {
		return nil, err
	}

	switch in.Direction {
	case domain.InterbankOutbound:
		return s.prepareOutbound(ctx, in, amt)
	case domain.InterbankInbound:
		return s.prepareInbound(ctx, in)
	default:
		return nil, apperr.Validation("unknown direction")
	}
}

// onPrepareFailure records a 'failed' audit row + bumps the consecutive-
// failure counter, auto-blocking the partner once the threshold is hit.
// Blacklist rejections don't re-count (the partner is already blocked).
// All legs are best-effort and never alter the surfaced error.
func (s *Service) onPrepareFailure(ctx context.Context, in PreparePaymentInput, cause error) {
	if in.SenderRoutingNumber == 0 || in.TransactionID == "" {
		return
	}
	if !isCountablePrepareFailure(cause) {
		// "partner bank is blacklisted" / "transaction previously failed"
		// — neither is a fresh partner failure to count.
		return
	}

	// Record the attempt as a 'failed' transaction row for the supervisor
	// status view. Direction/account/currency may be partially invalid;
	// store what we have so the row is still auditable.
	row := &domain.InterbankProtocolTransaction{
		SenderRoutingNumber: in.SenderRoutingNumber,
		TransactionID:       in.TransactionID,
		Direction:           in.Direction,
		LocalAccountNumber:  in.LocalAccountNumber,
		RemoteAccountNumber: in.RemoteAccountNumber,
		Currency:            in.Currency,
		Amount:              in.Amount,
		Purpose:             in.Purpose,
		TransactionBody:     in.TransactionBody,
		Status:              domain.InterbankTxFailed,
		LastError:           cause.Error(),
	}
	if ierr := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		_, e := s.Store.InsertInterbankTx(ctx, tx, row)
		return e
	}); ierr != nil {
		// A conflict means a row already exists (e.g. a partial prepare
		// that did persist); not worth surfacing.
		s.Log.Warn("interbank: record failed attempt", "sender_routing_number", in.SenderRoutingNumber, "transaction_id", in.TransactionID, "error", ierr)
	}

	n, ferr := s.Store.RecordPartnerFailure(ctx, in.SenderRoutingNumber)
	if ferr != nil {
		s.Log.Warn("interbank: record partner failure", "sender_routing_number", in.SenderRoutingNumber, "error", ferr)
		return
	}
	if shouldAutoBlock(n) {
		s.autoBlockPartner(ctx, in.SenderRoutingNumber, n)
	}
}

// shouldAutoBlock reports whether a consecutive-failure count trips the
// auto-block threshold. Pure so the policy is unit-testable without a DB.
func shouldAutoBlock(consecutiveFailures int) bool {
	return consecutiveFailures >= domain.InterbankFailureThreshold
}

// isCountablePrepareFailure reports whether a prepare error should bump
// the partner's failure counter. Blacklist / already-failed rejections
// (FailedPrecondition) don't count — the partner is already blocked or
// the attempt was already recorded. Pure for unit-testability.
func isCountablePrepareFailure(err error) bool {
	return !apperrIs(err, apperr.KindFailedPrecondition)
}

// autoBlockPartner blocks a routing number after too many consecutive
// failures and notifies a supervisor. Best-effort.
func (s *Service) autoBlockPartner(ctx context.Context, senderRouting, failures int) {
	reason := fmt.Sprintf("automatski blokirana posle %d uzastopnih neuspeha", failures)
	if _, err := s.Store.BlockBank(ctx, senderRouting, reason, domain.BlacklistAutoBlockBy); err != nil {
		s.Log.Warn("interbank: auto-block partner", "sender_routing_number", senderRouting, "error", err)
		return
	}
	s.Log.Warn("interbank: partner auto-blacklisted",
		"sender_routing_number", senderRouting, "consecutive_failures", failures)
	s.notifyInterbankAutoBlock(ctx, senderRouting, failures)
}

// notifyInterbankAutoBlock fans an in-app notice to the supervisors so
// the auto-block is visible in the portal. Email isn't used — there's no
// single supervisor address; the in-app feed keyed by the supervisor
// role is the surface. Best-effort.
func (s *Service) notifyInterbankAutoBlock(ctx context.Context, senderRouting, failures int) {
	if s.InApp == nil {
		return
	}
	title := "Partnerska banka automatski blokirana"
	body := fmt.Sprintf(
		"Banka sa rutnim brojem %d je automatski blokirana posle %d uzastopnih neuspelih međubankarskih transakcija. Proverite u portalu Međubankarske transakcije.",
		senderRouting, failures,
	)
	if err := s.InApp.Notify(ctx, domain.InterbankSupervisorAudience, "employee", "interbank", title, body); err != nil {
		s.Log.Warn("interbank: auto-block notify", "error", err)
	}
}

func (s *Service) prepareOutbound(ctx context.Context, in PreparePaymentInput, amt any) (*PreparePaymentResult, error) {
	// Resolve the source account by number; reject if absent / inactive
	// / wrong currency.
	src, err := s.Store.GetAccountByNumber(ctx, in.LocalAccountNumber)
	if err != nil {
		return nil, apperr.Validation("source account not found")
	}
	if src.Currency != in.Currency {
		return nil, apperr.Validation("currency mismatch on source account")
	}
	if src.Status != domain.AccountActive {
		return nil, apperr.FailedPrecondition("source account is not active")
	}

	opID := deriveOpID(in.SenderRoutingNumber, in.TransactionID)

	// Reserve funds — debits available_balance, writes bank.reservations.
	res, err := s.ReserveFunds(ctx, ReserveFundsInput{
		AccountID: src.ID,
		Amount:    in.Amount,
		Currency:  in.Currency,
		OpID:      opID,
		OpKind:    string(domain.TxKindInterbankPayment),
	})
	if err != nil {
		return nil, err
	}

	row := &domain.InterbankProtocolTransaction{
		SenderRoutingNumber: in.SenderRoutingNumber,
		TransactionID:       in.TransactionID,
		Direction:           domain.InterbankOutbound,
		LocalAccountNumber:  in.LocalAccountNumber,
		RemoteAccountNumber: in.RemoteAccountNumber,
		Currency:            in.Currency,
		Amount:              in.Amount,
		Purpose:             in.Purpose,
		TransactionBody:     in.TransactionBody,
		ReservationID:       res.ReservationID,
		Status:              domain.InterbankTxPrepared,
	}
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		_, ierr := s.Store.InsertInterbankTx(ctx, tx, row)
		return ierr
	}); err != nil {
		// Release the reservation if recording the prepared row failed —
		// avoids leaking a held debit.
		_, _ = s.ReleaseFunds(ctx, opID)
		return nil, err
	}
	_ = amt // already consumed by ReserveFunds via in.Amount
	return &PreparePaymentResult{
		TransactionID: in.TransactionID,
		Status:        domain.InterbankTxPrepared,
		ReservationID: res.ReservationID,
	}, nil
}

func (s *Service) prepareInbound(ctx context.Context, in PreparePaymentInput) (*PreparePaymentResult, error) {
	dst, err := s.Store.GetAccountByNumber(ctx, in.LocalAccountNumber)
	if err != nil {
		return nil, apperr.Validation("destination account not found")
	}
	if dst.Currency != in.Currency {
		return nil, apperr.Validation("currency mismatch on destination account")
	}
	if dst.Status != domain.AccountActive {
		return nil, apperr.FailedPrecondition("destination account is not active")
	}
	// Bank system account must exist for the inbound credit's debit
	// counterparty. Fail fast here so commit doesn't half-write.
	if _, err := s.Store.GetSystemAccount(ctx, in.Currency); err != nil {
		return nil, apperr.FailedPrecondition("bank system account missing for currency")
	}

	row := &domain.InterbankProtocolTransaction{
		SenderRoutingNumber: in.SenderRoutingNumber,
		TransactionID:       in.TransactionID,
		Direction:           domain.InterbankInbound,
		LocalAccountNumber:  in.LocalAccountNumber,
		RemoteAccountNumber: in.RemoteAccountNumber,
		Currency:            in.Currency,
		Amount:              in.Amount,
		Purpose:             in.Purpose,
		TransactionBody:     in.TransactionBody,
		Status:              domain.InterbankTxPrepared,
	}
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		_, ierr := s.Store.InsertInterbankTx(ctx, tx, row)
		return ierr
	}); err != nil {
		return nil, err
	}
	return &PreparePaymentResult{
		TransactionID: in.TransactionID,
		Status:        domain.InterbankTxPrepared,
	}, nil
}

// CommitPaymentResult is what the partner gets after COMMIT_TX.
type CommitPaymentResult struct {
	TransactionID string
	Status        domain.InterbankTxStatus
	OpID          string
}

// CommitPayment finalises a prepared transaction. Idempotent — replays
// on a committed row return the existing op_id; replays on a rolled-
// back row return FailedPrecondition.
func (s *Service) CommitPayment(ctx context.Context, senderRouting int, txID string) (*CommitPaymentResult, error) {
	if err := s.requireInternal(ctx); err != nil {
		return nil, err
	}
	if txID == "" || senderRouting == 0 {
		return nil, apperr.Validation("transaction_id and sender_routing_number are required")
	}

	row, err := s.Store.GetInterbankTx(ctx, nil, senderRouting, txID, false)
	if err != nil {
		return nil, err
	}
	switch row.Status {
	case domain.InterbankTxCommitted:
		return &CommitPaymentResult{TransactionID: txID, Status: row.Status, OpID: row.OpID}, nil
	case domain.InterbankTxRolledBack:
		return nil, apperr.FailedPrecondition("transaction is rolled back")
	case domain.InterbankTxPrepared:
		// fall through
	default:
		return nil, apperr.Internal("unknown interbank tx status", nil)
	}

	opID := deriveOpID(senderRouting, txID)

	switch row.Direction {
	case domain.InterbankOutbound:
		return s.commitOutbound(ctx, row, opID)
	case domain.InterbankInbound:
		return s.commitInbound(ctx, row, opID)
	default:
		return nil, apperr.Internal("unknown direction", nil)
	}
}

func (s *Service) commitOutbound(ctx context.Context, row *domain.InterbankProtocolTransaction, opID string) (*CommitPaymentResult, error) {
	// Destination is the bank's per-currency system house account —
	// money "leaves" the user and lands in the bank's clearing account
	// (the partner credits their user separately on their side).
	system, err := s.Store.GetSystemAccount(ctx, row.Currency)
	if err != nil {
		return nil, apperr.FailedPrecondition("bank system account missing for currency")
	}
	if _, err := s.CommitReservedFunds(ctx, CommitReservedFundsInput{
		OpID:          opID,
		DestAccountID: system.ID,
		DestAmount:    row.Amount,
		DestCurrency:  row.Currency,
		IsActuary:     true, // internal — no commission
		Purpose:       interbankPurpose(row),
	}); err != nil {
		return nil, err
	}
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		_, merr := s.Store.MarkInterbankTxStatus(ctx, tx, row.SenderRoutingNumber, row.TransactionID,
			domain.InterbankTxCommitted, opID, "")
		return merr
	}); err != nil {
		return nil, err
	}
	return &CommitPaymentResult{TransactionID: row.TransactionID, Status: domain.InterbankTxCommitted, OpID: opID}, nil
}

func (s *Service) commitInbound(ctx context.Context, row *domain.InterbankProtocolTransaction, opID string) (*CommitPaymentResult, error) {
	// Idempotent fast-path: if the transactions leg already exists for
	// this op_id, just flip the row's status.
	if legs, err := s.Store.GetTransactionsByOpID(ctx, opID); err == nil && len(legs) > 0 {
		if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
			_, merr := s.Store.MarkInterbankTxStatus(ctx, tx, row.SenderRoutingNumber, row.TransactionID,
				domain.InterbankTxCommitted, opID, "")
			return merr
		}); err != nil {
			return nil, err
		}
		return &CommitPaymentResult{TransactionID: row.TransactionID, Status: domain.InterbankTxCommitted, OpID: opID}, nil
	}

	dst, err := s.Store.GetAccountByNumber(ctx, row.LocalAccountNumber)
	if err != nil {
		return nil, apperr.Validation("destination account not found")
	}
	if dst.Status != domain.AccountActive {
		return nil, apperr.FailedPrecondition("destination account is not active")
	}
	system, err := s.Store.GetSystemAccount(ctx, row.Currency)
	if err != nil {
		return nil, apperr.FailedPrecondition("bank system account missing for currency")
	}
	amt, err := parsePositive(row.Amount)
	if err != nil {
		return nil, err
	}

	initiator := auth.Principal{Permissions: []string{permissions.Admin}}
	purpose := interbankPurpose(row)

	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		// Debit bank.system in the currency; credit the user's account.
		// Use the same executeMoneyMove engine the FX path uses (single
		// leg same-currency).
		_, mverr := s.executeMoneyMove(ctx, tx, system, dst, amt, domain.TxKindInterbankPayment, opID, initiator, paymentMeta{Purpose: purpose}, 0)
		if mverr != nil {
			return mverr
		}
		_, merr := s.Store.MarkInterbankTxStatus(ctx, tx, row.SenderRoutingNumber, row.TransactionID,
			domain.InterbankTxCommitted, opID, "")
		return merr
	}); err != nil {
		return nil, err
	}
	return &CommitPaymentResult{TransactionID: row.TransactionID, Status: domain.InterbankTxCommitted, OpID: opID}, nil
}

// RollbackPaymentResult — same shape as the others; status pins the
// terminal state.
type RollbackPaymentResult struct {
	TransactionID string
	Status        domain.InterbankTxStatus
}

// RollbackPayment releases a prepared transaction. Idempotent on
// already-rolled-back rows; FailedPrecondition on committed.
func (s *Service) RollbackPayment(ctx context.Context, senderRouting int, txID, reason string) (*RollbackPaymentResult, error) {
	if err := s.requireInternal(ctx); err != nil {
		return nil, err
	}
	if txID == "" || senderRouting == 0 {
		return nil, apperr.Validation("transaction_id and sender_routing_number are required")
	}

	row, err := s.Store.GetInterbankTx(ctx, nil, senderRouting, txID, false)
	if err != nil {
		return nil, err
	}
	switch row.Status {
	case domain.InterbankTxRolledBack:
		return &RollbackPaymentResult{TransactionID: txID, Status: row.Status}, nil
	case domain.InterbankTxCommitted:
		return nil, apperr.FailedPrecondition("transaction is already committed")
	case domain.InterbankTxPrepared:
		// fall through
	default:
		return nil, apperr.Internal("unknown interbank tx status", nil)
	}

	// Outbound: release the held reservation. Inbound: nothing to do.
	if row.Direction == domain.InterbankOutbound {
		opID := deriveOpID(senderRouting, txID)
		if _, err := s.ReleaseFunds(ctx, opID); err != nil && !apperrIs(err, apperr.KindNotFound) {
			return nil, err
		}
	}
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		_, merr := s.Store.MarkInterbankTxStatus(ctx, tx, row.SenderRoutingNumber, row.TransactionID,
			domain.InterbankTxRolledBack, "", reason)
		return merr
	}); err != nil {
		return nil, err
	}
	return &RollbackPaymentResult{TransactionID: txID, Status: domain.InterbankTxRolledBack}, nil
}

// RecordInboundMessageInput.
type RecordInboundMessageInput struct {
	SenderRoutingNumber int
	IdempotenceKey      string
	MessageType         domain.InterbankMessageType
	TransactionID       string
	ResponseStatus      int
	ResponseBody        string
}

// RecordInboundMessage stashes the response we returned to a partner
// so a replayed (sender, key) can be answered without re-running the
// underlying Prepare/Commit/Rollback.
func (s *Service) RecordInboundMessage(ctx context.Context, in RecordInboundMessageInput) error {
	if err := s.requireInternal(ctx); err != nil {
		return err
	}
	if in.SenderRoutingNumber == 0 || in.IdempotenceKey == "" {
		return apperr.Validation("sender_routing_number and idempotence_key are required")
	}
	row := &domain.InterbankProtocolMessage{
		SenderRoutingNumber: in.SenderRoutingNumber,
		IdempotenceKey:      in.IdempotenceKey,
		MessageType:         in.MessageType,
		TransactionID:       in.TransactionID,
		ResponseStatus:      in.ResponseStatus,
		ResponseBody:        in.ResponseBody,
	}
	return s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		return s.Store.UpsertInterbankMessage(ctx, tx, row)
	})
}

// GetInboundMessage returns the cached response for a (sender, key) or
// nil if this is a first-time message.
func (s *Service) GetInboundMessage(ctx context.Context, senderRouting int, key string) (*domain.InterbankProtocolMessage, error) {
	if err := s.requireInternal(ctx); err != nil {
		return nil, err
	}
	out, err := s.Store.GetInterbankMessage(ctx, senderRouting, key)
	if err != nil {
		if apperrIs(err, apperr.KindNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return out, nil
}

// interbankPurpose builds the human-readable "Purpose" string copied
// onto each interbank ledger leg. Kept short and stable so SAGA logs +
// pregled plaćanja can grep it.
func interbankPurpose(row *domain.InterbankProtocolTransaction) string {
	if row.Purpose != "" {
		return row.Purpose
	}
	return fmt.Sprintf("interbank %s %d:%s", row.Direction, row.SenderRoutingNumber, row.TransactionID)
}

// ErrInterbankUnsupported is wired here so unit tests can match on the
// stable sentinel value rather than the message string.
var ErrInterbankUnsupported = errors.New("interbank: unsupported")
