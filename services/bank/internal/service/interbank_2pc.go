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
		if clientClassErr(err) {
			s.log().WarnContext(ctx, "interbank prepare rejected",
				"err", err, "transaction_id", in.TransactionID,
				"sender_routing_number", in.SenderRoutingNumber, "direction", in.Direction,
				"local_account", in.LocalAccountNumber, "remote_account", in.RemoteAccountNumber,
				"amount", in.Amount, "currency", in.Currency)
		} else {
			s.log().ErrorContext(ctx, "interbank prepare failed",
				"err", err, "transaction_id", in.TransactionID,
				"sender_routing_number", in.SenderRoutingNumber, "direction", in.Direction,
				"local_account", in.LocalAccountNumber, "remote_account", in.RemoteAccountNumber,
				"amount", in.Amount, "currency", in.Currency)
		}
		return nil, err
	}
	// A clean prepare clears any prior failure streak.
	if rerr := s.Store.ResetPartnerFailures(ctx, in.SenderRoutingNumber); rerr != nil {
		s.log().WarnContext(ctx, "interbank: reset partner failures", "err", rerr, "sender_routing_number", in.SenderRoutingNumber)
	}
	s.log().InfoContext(ctx, "interbank tx prepared",
		"transaction_id", res.TransactionID, "status", res.Status, "reservation_id", res.ReservationID,
		"sender_routing_number", in.SenderRoutingNumber, "direction", in.Direction,
		"local_account", in.LocalAccountNumber, "remote_account", in.RemoteAccountNumber,
		"amount", in.Amount, "currency", in.Currency)
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

	// local_account_number is the account whose funds actually move and must
	// be a real 18-digit number. remote_account_number is audit-only (see the
	// proto comment): 18 digits for cash payments, empty for PERSON-addressed
	// OTC settlements where the counterparty has no account number.
	if len(in.LocalAccountNumber) != 18 {
		return nil, apperr.Validation("local account number must be 18 digits")
	}
	if in.RemoteAccountNumber != "" && len(in.RemoteAccountNumber) != 18 {
		return nil, apperr.Validation("remote account number must be 18 digits or empty")
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
		s.log().WarnContext(ctx, "interbank: record failed attempt", "err", ierr, "sender_routing_number", in.SenderRoutingNumber, "transaction_id", in.TransactionID)
	}

	n, ferr := s.Store.RecordPartnerFailure(ctx, in.SenderRoutingNumber)
	if ferr != nil {
		s.log().WarnContext(ctx, "interbank: record partner failure", "err", ferr, "sender_routing_number", in.SenderRoutingNumber)
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
		s.log().ErrorContext(ctx, "interbank: auto-block partner failed", "err", err, "sender_routing_number", senderRouting)
		return
	}
	s.log().WarnContext(ctx, "interbank: partner auto-blacklisted",
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
		s.log().WarnContext(ctx, "interbank: auto-block notify failed", "err", err, "sender_routing_number", senderRouting)
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
		s.log().ErrorContext(ctx, "interbank prepare outbound: record prepared row failed, releasing reservation",
			"err", err, "transaction_id", in.TransactionID, "sender_routing_number", in.SenderRoutingNumber,
			"reservation_id", res.ReservationID, "op_id", opID,
			"local_account", in.LocalAccountNumber, "amount", in.Amount, "currency", in.Currency)
		// Release the reservation if recording the prepared row failed —
		// avoids leaking a held debit.
		if _, rerr := s.ReleaseFunds(ctx, opID); rerr != nil {
			s.log().ErrorContext(ctx, "interbank prepare outbound: release reservation failed",
				"err", rerr, "transaction_id", in.TransactionID, "sender_routing_number", in.SenderRoutingNumber,
				"reservation_id", res.ReservationID, "op_id", opID)
		}
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
		if apperrIs(err, apperr.KindNotFound) {
			s.log().WarnContext(ctx, "interbank commit: tx not found",
				"err", err, "transaction_id", txID, "sender_routing_number", senderRouting)
		} else {
			s.log().ErrorContext(ctx, "interbank commit: tx lookup failed",
				"err", err, "transaction_id", txID, "sender_routing_number", senderRouting)
		}
		return nil, err
	}
	switch row.Status {
	case domain.InterbankTxCommitted:
		return &CommitPaymentResult{TransactionID: txID, Status: row.Status, OpID: row.OpID}, nil
	case domain.InterbankTxRolledBack:
		s.log().WarnContext(ctx, "interbank commit rejected: transaction is rolled back",
			"transaction_id", txID, "sender_routing_number", senderRouting, "direction", row.Direction)
		return nil, apperr.FailedPrecondition("transaction is rolled back")
	case domain.InterbankTxPrepared:
		// fall through
	default:
		s.log().ErrorContext(ctx, "interbank commit: unknown tx status",
			"transaction_id", txID, "sender_routing_number", senderRouting, "status", row.Status)
		return nil, apperr.Internal("unknown interbank tx status", nil)
	}

	opID := deriveOpID(senderRouting, txID)

	switch row.Direction {
	case domain.InterbankOutbound:
		return s.commitOutbound(ctx, row, opID)
	case domain.InterbankInbound:
		return s.commitInbound(ctx, row, opID)
	default:
		s.log().ErrorContext(ctx, "interbank commit: unknown direction",
			"transaction_id", txID, "sender_routing_number", senderRouting, "direction", row.Direction)
		return nil, apperr.Internal("unknown direction", nil)
	}
}

func (s *Service) commitOutbound(ctx context.Context, row *domain.InterbankProtocolTransaction, opID string) (*CommitPaymentResult, error) {
	// Destination is the bank's per-currency system house account —
	// money "leaves" the user and lands in the bank's clearing account
	// (the partner credits their user separately on their side).
	system, err := s.Store.GetSystemAccount(ctx, row.Currency)
	if err != nil {
		s.log().ErrorContext(ctx, "interbank commit outbound: bank system account missing",
			"err", err, "transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
			"currency", row.Currency)
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
		s.log().ErrorContext(ctx, "interbank commit outbound: commit reserved funds failed",
			"err", err, "transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
			"op_id", opID, "reservation_id", row.ReservationID, "direction", row.Direction,
			"local_account", row.LocalAccountNumber, "remote_account", row.RemoteAccountNumber,
			"amount", row.Amount, "currency", row.Currency)
		return nil, err
	}
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		_, merr := s.Store.MarkInterbankTxStatus(ctx, tx, row.SenderRoutingNumber, row.TransactionID,
			domain.InterbankTxCommitted, opID, "")
		return merr
	}); err != nil {
		// Money has moved but the row still reads 'prepared' — flag loudly
		// so a stuck-looking 2PC can be traced to this exact spot.
		s.log().ErrorContext(ctx, "interbank commit outbound: funds committed but status update failed",
			"err", err, "transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
			"op_id", opID, "reservation_id", row.ReservationID,
			"amount", row.Amount, "currency", row.Currency)
		return nil, err
	}
	s.log().InfoContext(ctx, "interbank tx committed",
		"transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
		"direction", row.Direction, "op_id", opID, "reservation_id", row.ReservationID,
		"local_account", row.LocalAccountNumber, "remote_account", row.RemoteAccountNumber,
		"amount", row.Amount, "currency", row.Currency)
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
			// Ledger legs exist but the row still reads 'prepared' —
			// inconsistent state worth a loud record.
			s.log().ErrorContext(ctx, "interbank commit inbound: legs exist but status update failed",
				"err", err, "transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
				"op_id", opID, "amount", row.Amount, "currency", row.Currency)
			return nil, err
		}
		s.log().InfoContext(ctx, "interbank tx committed",
			"transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
			"direction", row.Direction, "op_id", opID,
			"local_account", row.LocalAccountNumber, "remote_account", row.RemoteAccountNumber,
			"amount", row.Amount, "currency", row.Currency)
		return &CommitPaymentResult{TransactionID: row.TransactionID, Status: domain.InterbankTxCommitted, OpID: opID}, nil
	}

	dst, err := s.Store.GetAccountByNumber(ctx, row.LocalAccountNumber)
	if err != nil {
		// Prepare already validated this account; vanishing at commit
		// time is an inconsistency, not a client mistake.
		s.log().ErrorContext(ctx, "interbank commit inbound: destination account lookup failed",
			"err", err, "transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
			"local_account", row.LocalAccountNumber)
		return nil, apperr.Validation("destination account not found")
	}
	if dst.Status != domain.AccountActive {
		s.log().WarnContext(ctx, "interbank commit inbound: destination account not active",
			"transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
			"local_account", row.LocalAccountNumber, "account_status", dst.Status)
		return nil, apperr.FailedPrecondition("destination account is not active")
	}
	system, err := s.Store.GetSystemAccount(ctx, row.Currency)
	if err != nil {
		s.log().ErrorContext(ctx, "interbank commit inbound: bank system account missing",
			"err", err, "transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
			"currency", row.Currency)
		return nil, apperr.FailedPrecondition("bank system account missing for currency")
	}
	amt, err := parsePositive(row.Amount)
	if err != nil {
		// The stored amount was validated at prepare time; failing to
		// parse now means the row is corrupt.
		s.log().ErrorContext(ctx, "interbank commit inbound: stored amount unparseable",
			"err", err, "transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
			"amount", row.Amount, "currency", row.Currency)
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
		s.log().ErrorContext(ctx, "interbank commit inbound failed",
			"err", err, "transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
			"op_id", opID, "direction", row.Direction,
			"local_account", row.LocalAccountNumber, "remote_account", row.RemoteAccountNumber,
			"amount", row.Amount, "currency", row.Currency)
		return nil, err
	}
	s.log().InfoContext(ctx, "interbank tx committed",
		"transaction_id", row.TransactionID, "sender_routing_number", row.SenderRoutingNumber,
		"direction", row.Direction, "op_id", opID,
		"local_account", row.LocalAccountNumber, "remote_account", row.RemoteAccountNumber,
		"amount", row.Amount, "currency", row.Currency)
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
		if apperrIs(err, apperr.KindNotFound) {
			s.log().WarnContext(ctx, "interbank rollback: tx not found",
				"err", err, "transaction_id", txID, "sender_routing_number", senderRouting)
		} else {
			s.log().ErrorContext(ctx, "interbank rollback: tx lookup failed",
				"err", err, "transaction_id", txID, "sender_routing_number", senderRouting)
		}
		return nil, err
	}
	switch row.Status {
	case domain.InterbankTxRolledBack:
		return &RollbackPaymentResult{TransactionID: txID, Status: row.Status}, nil
	case domain.InterbankTxCommitted:
		s.log().WarnContext(ctx, "interbank rollback rejected: transaction is already committed",
			"transaction_id", txID, "sender_routing_number", senderRouting, "direction", row.Direction)
		return nil, apperr.FailedPrecondition("transaction is already committed")
	case domain.InterbankTxPrepared:
		// fall through
	default:
		s.log().ErrorContext(ctx, "interbank rollback: unknown tx status",
			"transaction_id", txID, "sender_routing_number", senderRouting, "status", row.Status)
		return nil, apperr.Internal("unknown interbank tx status", nil)
	}

	// Outbound: release the held reservation. Inbound: nothing to do.
	if row.Direction == domain.InterbankOutbound {
		opID := deriveOpID(senderRouting, txID)
		if _, err := s.ReleaseFunds(ctx, opID); err != nil && !apperrIs(err, apperr.KindNotFound) {
			s.log().ErrorContext(ctx, "interbank rollback: release reservation failed",
				"err", err, "transaction_id", txID, "sender_routing_number", senderRouting,
				"op_id", opID, "reservation_id", row.ReservationID,
				"amount", row.Amount, "currency", row.Currency)
			return nil, err
		}
	}
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		_, merr := s.Store.MarkInterbankTxStatus(ctx, tx, row.SenderRoutingNumber, row.TransactionID,
			domain.InterbankTxRolledBack, "", reason)
		return merr
	}); err != nil {
		// Reservation (if any) is released but the row still reads
		// 'prepared' — inconsistent state worth a loud record.
		s.log().ErrorContext(ctx, "interbank rollback: reservation released but status update failed",
			"err", err, "transaction_id", txID, "sender_routing_number", senderRouting,
			"direction", row.Direction, "amount", row.Amount, "currency", row.Currency)
		return nil, err
	}
	s.log().InfoContext(ctx, "interbank tx rolled back",
		"transaction_id", txID, "sender_routing_number", senderRouting,
		"direction", row.Direction, "reservation_id", row.ReservationID,
		"local_account", row.LocalAccountNumber, "remote_account", row.RemoteAccountNumber,
		"amount", row.Amount, "currency", row.Currency, "reason", reason)
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
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		return s.Store.UpsertInterbankMessage(ctx, tx, row)
	}); err != nil {
		s.log().ErrorContext(ctx, "interbank record inbound message failed",
			"err", err, "sender_routing_number", in.SenderRoutingNumber,
			"idempotence_key", in.IdempotenceKey, "message_type", in.MessageType,
			"transaction_id", in.TransactionID)
		return err
	}
	return nil
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
		s.log().ErrorContext(ctx, "interbank get inbound message failed",
			"err", err, "sender_routing_number", senderRouting, "idempotence_key", key)
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
