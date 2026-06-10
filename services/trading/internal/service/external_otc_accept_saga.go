// External OTC accept SAGA (celina 5 — spec p.77+).
//
// Cross-bank counterpart of otc_accept_saga.go. Only OUTGOING threads
// run this — the local user is the buyer, the partner is the seller.
// Incoming threads' "accept" is the partner-driven inbound REST hit
// (ReceiveExternalOTCAccept in external_otc.go) which only flips
// status + mints a contract; the partner's bank drives the 2PC for
// the premium leg into us separately.
//
// Flow (outgoing — we are buyer):
//   1. prepare_premium — bank.PreparePayment(outbound) reserves the
//      buyer's premium in their local account; bank.reservations row
//      lands in 'held' state.
//   2. notify_partner_accept — call PartnerOTC.Accept(remote_bank, …)
//      so the partner mints their contract + accepts the offer on
//      their side. A 4xx here triggers compensation (rollback the
//      reservation).
//   3. commit_premium — bank.CommitPayment finalises the prepared
//      transaction; the premium "leaves" the buyer and lands in
//      bank.system_<currency> on our books (the partner credits their
//      seller separately on their side).
//   4. create_local_contract — insert external_otc_contracts +
//      flip the thread to 'accepted'.
//
// Idempotency
// ===========
// The saga's transaction_id is derived from the local thread id; a
// retry resumes via saga.Start's idempotency. Each bank-side call
// uses the derived c5 op_id from (sender_routing, tx_id) — bank-side
// unique constraints on op_id are the backstop.

package service

import (
	"context"
	"fmt"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const externalOTCAcceptSagaType = "external_otc_accept"

type externalOTCAcceptPayload struct {
	ThreadID            string `json:"thread_id"`
	RemoteBankCode      string `json:"remote_bank_code"`
	RemoteThreadID      string `json:"remote_thread_id"`
	RemoteUserRef       string `json:"remote_user_ref"`
	RemoteDisplayName   string `json:"remote_display_name"`
	RemoteAccountRef    string `json:"remote_account_ref"`
	LocalUserID         string `json:"local_user_id"`
	LocalUserKind       string `json:"local_user_kind"`
	LocalAccountID      string `json:"local_account_id"`
	LocalAccountNumber  string `json:"local_account_number"`
	SecurityID          string `json:"security_id"`
	SecurityTicker      string `json:"security_ticker"`
	SellerHoldingRef    string `json:"seller_holding_ref"`
	Quantity            int32  `json:"quantity"`
	PricePerUnit        string `json:"price_per_unit"`
	Premium             string `json:"premium"`
	Currency            string `json:"currency"`
	SettlementDate      string `json:"settlement_date"`
	SenderRoutingNumber int    `json:"sender_routing_number"`

	// PartnerCoordinatesAccept is true when the partner's bank coordinates
	// the accept 2PC (si-tx-proto / Banka-2). Then prepare_premium and
	// commit_premium are no-ops — the partner debits our buyer via an
	// inbound NEW_TX; we only notify accept + record the local contract.
	PartnerCoordinatesAccept bool `json:"partner_coordinates_accept"`
}

var externalOTCAcceptNS = uuid.MustParse("c5ac0030-7e62-4f00-9c1d-1f24f2d8a401")

// externalOTCAcceptTxID derives a deterministic saga transaction_id +
// inter-bank tx_id from the local thread id. The bank-side
// InterbankProtocolService PK is (sender_routing_number, transaction_id);
// using the thread-id-derived string keeps both stable for retries.
func externalOTCAcceptTxID(threadID string) string {
	return uuid.NewSHA1(externalOTCAcceptNS, []byte(threadID)).String()
}

// AcceptExternalOTCOffer kicks off the cross-bank accept saga for an
// outgoing thread. Caller must be the local user-of-record on the
// thread (the buyer in the outgoing case).
func (s *Service) acceptExternalOutgoing(ctx context.Context, thread *domain.ExternalOTCThread) (*AcceptExternalOTCOfferResult, error) {
	if s.SagaOrch == nil || s.InterbankPayer == nil || s.PartnerOTC == nil {
		return nil, apperr.FailedPrecondition("cross-bank infrastructure nije konfigurisana")
	}
	// Spec edge: we can only accept a thread the partner moved last
	// (modified_by_side = remote). Otherwise the buyer's accepting their
	// own iteration.
	if thread.ModifiedBySide != domain.ExternalOTCSideRemote {
		s.log().WarnContext(ctx, "external otc accept rejected: not remote side's turn",
			"thread_id", thread.ID, "remote_bank_code", thread.RemoteBankCode)
		return nil, apperr.FailedPrecondition("druga strana je na potezu")
	}
	if thread.Status != domain.ExternalOTCThreadOpen {
		s.log().WarnContext(ctx, "external otc accept rejected: thread not open",
			"thread_id", thread.ID, "status", string(thread.Status),
			"remote_bank_code", thread.RemoteBankCode)
		return nil, apperr.FailedPrecondition("nit nije otvorena")
	}
	if thread.LocalRole != domain.ExternalOTCRoleBuyer {
		s.log().WarnContext(ctx, "external otc accept rejected: local side is not buyer",
			"thread_id", thread.ID, "local_role", string(thread.LocalRole),
			"remote_bank_code", thread.RemoteBankCode)
		return nil, apperr.FailedPrecondition("samo kupac može prihvatiti eksternu ponudu (za prodavca se koristi Receive*)")
	}

	txID := externalOTCAcceptTxID(thread.ID)

	payload := externalOTCAcceptPayload{
		ThreadID:            thread.ID,
		RemoteBankCode:      thread.RemoteBankCode,
		RemoteThreadID:      thread.RemoteThreadID,
		RemoteUserRef:       thread.RemoteUserRef,
		RemoteDisplayName:   thread.RemoteDisplayName,
		RemoteAccountRef:    thread.RemoteAccountRef,
		LocalUserID:         thread.LocalUserID,
		LocalUserKind:       string(thread.LocalUserKind),
		LocalAccountID:      thread.LocalAccountID,
		LocalAccountNumber:  thread.LocalAccountNumber,
		SecurityID:          thread.SecurityID,
		SecurityTicker:      thread.SecurityTicker,
		SellerHoldingRef:    thread.SellerHoldingRef,
		Quantity:            thread.Quantity,
		PricePerUnit:        thread.PricePerUnit,
		Premium:             thread.Premium,
		Currency:            string(thread.Currency),
		SettlementDate:      thread.SettlementDate.Format("2006-01-02"),
		SenderRoutingNumber: s.Cfg.OwnRoutingNumber,
		// si-tx-proto / Banka-2 partners coordinate the accept 2PC from the
		// seller side, debiting our buyer via an inbound NEW_TX. Detect once
		// here and persist in the saga state so the premium steps stay
		// no-ops across retries.
		PartnerCoordinatesAccept: s.PartnerOTC.AcceptCoordinatedByPartner(ctx, thread.RemoteBankCode),
	}

	ctx = saga.FaultsFromMetadata(ctx, s.Cfg.SagaDebugFaultInjection)
	row, err := saga.Start(ctx, s.SagaOrch, saga.StartInput[externalOTCAcceptPayload]{
		TransactionID: txID,
		SagaType:      externalOTCAcceptSagaType,
		InitialState:  payload,
		AttemptsMax:   8,
	})
	if err != nil {
		s.log().ErrorContext(ctx, "external otc accept saga failed",
			"err", err, "transaction_id", txID, "thread_id", thread.ID,
			"remote_bank_code", thread.RemoteBankCode)
		return nil, fmt.Errorf("external otc accept saga: %w", err)
	}
	if row.Status != saga.StatusCompleted {
		if row.Status == saga.StatusRunning {
			s.log().WarnContext(ctx, "external otc accept saga parked for retry",
				"transaction_id", txID, "thread_id", thread.ID,
				"remote_bank_code", thread.RemoteBankCode, "last_error", row.LastError)
			return nil, status.Error(codes.Unavailable, "external otc accept saga parked for retry")
		}
		s.log().ErrorContext(ctx, "external otc accept saga did not complete",
			"transaction_id", txID, "thread_id", thread.ID,
			"remote_bank_code", thread.RemoteBankCode,
			"saga_status", string(row.Status), "last_error", row.LastError)
		return nil, apperr.Internal("external otc accept saga did not complete", nil)
	}

	tFresh, err := s.Store.GetExternalOTCThread(ctx, thread.ID)
	if err != nil {
		s.logOpErr(ctx, "external otc accept: thread refetch failed", err,
			"thread_id", thread.ID, "transaction_id", txID)
		return nil, err
	}
	contract, err := s.Store.GetExternalOTCContractByThread(ctx, thread.ID)
	if err != nil {
		s.logOpErr(ctx, "external otc accept: contract fetch after saga failed", err,
			"thread_id", thread.ID, "transaction_id", txID)
		return nil, err
	}
	s.log().InfoContext(ctx, "external otc accept saga completed; contract minted",
		"transaction_id", txID, "thread_id", thread.ID, "contract_id", contract.ID,
		"remote_bank_code", thread.RemoteBankCode, "ticker", thread.SecurityTicker,
		"quantity", thread.Quantity)
	return &AcceptExternalOTCOfferResult{Thread: tFresh, Contract: contract}, nil
}

// registerExternalOTCAcceptSaga registers the saga at boot.
func registerExternalOTCAcceptSaga(reg *saga.Registry, svc *Service) {
	def := saga.Definition[externalOTCAcceptPayload]{
		Type: externalOTCAcceptSagaType,
		Steps: []saga.Step[externalOTCAcceptPayload]{
			// 1. Prepare the premium leg on our side.
			{
				Name: "prepare_premium",
				Forward: func(ctx context.Context, sc *saga.Context[externalOTCAcceptPayload]) error {
					if sc.State.PartnerCoordinatesAccept {
						// Partner (si-tx-proto/Banka-2) debits our buyer via an
						// inbound NEW_TX during notify_partner_accept; nothing to
						// prepare on our side.
						sc.Log.DebugContext(ctx, "external otc accept: premium prepare skipped (partner coordinates)",
							"thread_id", sc.State.ThreadID, "remote_bank_code", sc.State.RemoteBankCode)
						return nil
					}
					sc.Log.DebugContext(ctx, "external otc accept: preparing premium leg",
						"thread_id", sc.State.ThreadID, "remote_bank_code", sc.State.RemoteBankCode,
						"premium", sc.State.Premium, "currency", sc.State.Currency)
					_, err := svc.InterbankPayer.PreparePayment(ctx, PrepareInterbankInput{
						SenderRoutingNumber: sc.State.SenderRoutingNumber,
						TransactionID:       sc.TransactionID,
						LocalAccountNumber:  sc.State.LocalAccountNumber,
						RemoteAccountNumber: sc.State.RemoteAccountRef,
						Currency:            domain.Currency(sc.State.Currency),
						Amount:              sc.State.Premium,
						Purpose:             "OTC premija (eksterno) — nit " + sc.State.ThreadID,
					})
					if err != nil {
						sc.Log.ErrorContext(ctx, "external otc accept: premium prepare failed",
							"err", err, "thread_id", sc.State.ThreadID,
							"remote_bank_code", sc.State.RemoteBankCode)
					}
					return err
				},
				Compensate: func(ctx context.Context, sc *saga.Context[externalOTCAcceptPayload]) error {
					if sc.State.PartnerCoordinatesAccept {
						return nil
					}
					if err := svc.InterbankPayer.RollbackPayment(ctx,
						sc.State.SenderRoutingNumber, sc.TransactionID,
						"external otc accept compensation"); err != nil {
						sc.Log.ErrorContext(ctx, "external otc accept: premium rollback compensation failed",
							"err", err, "thread_id", sc.State.ThreadID,
							"remote_bank_code", sc.State.RemoteBankCode)
						return err
					}
					sc.Log.InfoContext(ctx, "external otc accept: premium reservation rolled back",
						"thread_id", sc.State.ThreadID, "remote_bank_code", sc.State.RemoteBankCode)
					return nil
				},
			},
			// 2. Tell the partner we're accepting. They mint a contract
			//    on their side and ack. A 4xx surfaces as a permanent
			//    error and compensates step 1.
			{
				Name: "notify_partner_accept",
				Forward: func(ctx context.Context, sc *saga.Context[externalOTCAcceptPayload]) error {
					settle, perr := time.Parse("2006-01-02", sc.State.SettlementDate)
					if perr != nil {
						sc.Log.ErrorContext(ctx, "external otc accept: bad settlement_date in saga state",
							"err", perr, "thread_id", sc.State.ThreadID,
							"settlement_date", sc.State.SettlementDate)
						return status.Error(codes.InvalidArgument, "bad settlement_date")
					}
					if err := svc.PartnerOTC.Accept(ctx, PartnerActionInput{
						RemoteBankCode: sc.State.RemoteBankCode,
						RemoteThreadID: sc.State.RemoteThreadID,
						LocalThreadID:  sc.State.ThreadID,
						Quantity:       sc.State.Quantity,
						PricePerUnit:   sc.State.PricePerUnit,
						Premium:        sc.State.Premium,
						SettlementDate: settle,
					}); err != nil {
						sc.Log.ErrorContext(ctx, "external otc accept: partner accept notification failed",
							"err", err, "thread_id", sc.State.ThreadID,
							"remote_bank_code", sc.State.RemoteBankCode,
							"remote_thread_id", sc.State.RemoteThreadID)
						return err
					}
					sc.Log.InfoContext(ctx, "external otc accept: partner acked accept",
						"thread_id", sc.State.ThreadID, "remote_bank_code", sc.State.RemoteBankCode,
						"remote_thread_id", sc.State.RemoteThreadID)
					return nil
				},
				// No partner-side compensation primitive — we'd have to
				// re-counter with the prior terms or call Withdraw. For
				// now, log and continue; the local-side rollback
				// (step 1 compensation) refunds the buyer.
				Compensate: nil,
			},
			// 3. Finalise our side. The bank's per-currency system
			//    account absorbs the premium; the partner credits their
			//    seller on their side.
			{
				Name: "commit_premium",
				Forward: func(ctx context.Context, sc *saga.Context[externalOTCAcceptPayload]) error {
					if sc.State.PartnerCoordinatesAccept {
						// Premium was settled by the partner-coordinated inbound
						// NEW_TX; nothing to commit on our side.
						return nil
					}
					_, err := svc.InterbankPayer.CommitPayment(ctx,
						sc.State.SenderRoutingNumber, sc.TransactionID)
					if err != nil {
						sc.Log.ErrorContext(ctx, "external otc accept: premium commit failed",
							"err", err, "thread_id", sc.State.ThreadID,
							"remote_bank_code", sc.State.RemoteBankCode)
						return err
					}
					sc.Log.InfoContext(ctx, "external otc accept: premium committed",
						"thread_id", sc.State.ThreadID, "remote_bank_code", sc.State.RemoteBankCode,
						"premium", sc.State.Premium, "currency", sc.State.Currency)
					return nil
				},
				// Commit's already moved money; rollback isn't safe.
				// Compensation here would need a refund-in-the-opposite-
				// direction interbank tx. Beyond scope for BE-7.
				Compensate: nil,
			},
			// 4. Insert the local contract; flip thread to accepted.
			{
				Name: "create_local_contract",
				Forward: func(ctx context.Context, sc *saga.Context[externalOTCAcceptPayload]) error {
					settle, perr := time.Parse("2006-01-02", sc.State.SettlementDate)
					if perr != nil {
						sc.Log.ErrorContext(ctx, "external otc accept: bad settlement_date in saga state",
							"err", perr, "thread_id", sc.State.ThreadID,
							"settlement_date", sc.State.SettlementDate)
						return status.Error(codes.InvalidArgument, "bad settlement_date")
					}
					err := svc.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
						_, ierr := svc.Store.InsertExternalOTCContract(ctx, tx, &domain.ExternalOTCContract{
							ThreadID:           sc.State.ThreadID,
							Direction:          domain.ExternalOTCOutgoing,
							RemoteBankCode:     sc.State.RemoteBankCode,
							RemoteThreadID:     sc.State.RemoteThreadID,
							RemoteUserRef:      sc.State.RemoteUserRef,
							RemoteDisplayName:  sc.State.RemoteDisplayName,
							RemoteAccountRef:   sc.State.RemoteAccountRef,
							LocalUserID:        sc.State.LocalUserID,
							LocalUserKind:      domain.UserKind(sc.State.LocalUserKind),
							LocalAccountID:     sc.State.LocalAccountID,
							LocalAccountNumber: sc.State.LocalAccountNumber,
							LocalRole:          domain.ExternalOTCRoleBuyer,
							SecurityID:         sc.State.SecurityID,
							SecurityTicker:     sc.State.SecurityTicker,
							SellerHoldingRef:   sc.State.SellerHoldingRef,
							Quantity:           sc.State.Quantity,
							StrikePrice:        sc.State.PricePerUnit,
							PremiumPaid:        sc.State.Premium,
							Currency:           domain.Currency(sc.State.Currency),
							SettlementDate:     settle,
							AcceptedBySide:     domain.ExternalOTCSideLocal,
							Status:             domain.ExternalOTCContractActive,
							PremiumOpID:        deriveExternalCommitOpID(sc.State.SenderRoutingNumber, sc.TransactionID),
						})
						if ierr != nil {
							return ierr
						}
						_, err := svc.Store.SetExternalOTCThreadStatus(ctx, tx, sc.State.ThreadID,
							domain.ExternalOTCThreadAccepted)
						return err
					})
					if err != nil {
						sc.Log.ErrorContext(ctx, "external otc accept: local contract create failed",
							"err", err, "thread_id", sc.State.ThreadID,
							"remote_bank_code", sc.State.RemoteBankCode)
						return err
					}
					sc.Log.InfoContext(ctx, "external otc accept: local contract created, thread accepted",
						"thread_id", sc.State.ThreadID, "remote_bank_code", sc.State.RemoteBankCode,
						"ticker", sc.State.SecurityTicker, "quantity", sc.State.Quantity)
					return nil
				},
				// Compensation: delete the contract; flip the thread back
				// to open. The bank-side legs are already committed; not
				// reversible at this layer.
				Compensate: nil,
			},
		},
	}
	saga.Register(reg, def)
}

// deriveExternalCommitOpID mirrors bank's deriveOpID — the bank.
// transactions row written at commit time uses this op_id, and we
// want to stamp it on the contract so the audit trail is intact.
func deriveExternalCommitOpID(senderRouting int, txID string) string {
	return uuid.NewSHA1(externalCommitOpIDNS,
		[]byte(fmt.Sprintf("%d:%s", senderRouting, txID))).String()
}

// externalCommitOpIDNS must match bank/service/interbank_2pc.go's
// c5Namespace constant so the two sides agree on the derived op_id.
// Keeping them in sync by value (not by import) avoids pulling the
// bank package into trading. If you change one, change the other.
var externalCommitOpIDNS = uuid.MustParse("a0b8e21b-3f0a-4f7a-8a0e-9b8a37c54a14")
