// External OTC exercise SAGA (celina 5 — spec p.80, cross-bank).
//
// Cross-bank counterpart of otc_exercise_saga.go. Only the buyer-side
// (outgoing contract) drives this saga; on the seller side the
// partner sends us a notice via ReceiveExternalOTCExerciseNotice and
// the cash leg arrives via the partner's bank.CommitPayment hitting
// our InterbankProtocolService.
//
// Flow (outgoing buyer exercises):
//   1. prepare_strike — bank.PreparePayment(outbound) reserves
//      qty * strike on the buyer's local account.
//   2. notify_partner_exercise — call PartnerOTC.Accept's exercise
//      counterpart… wait, we don't have one. Use a generic notify;
//      until BE-4c lands the dedicated exercise-notice outbound, this
//      step assumes the partner accepts the strike-leg 2PC commit
//      that lands in step 3. See "Partner notification" comment below.
//   3. commit_strike — bank.CommitPayment finalises the strike-leg
//      debit; funds land in bank.system_<currency>.
//   4. mark_exercised — flip contract status to 'exercised', stamp
//      exercise_op_id + exercised_at.
//
// Securities movement: not modelled locally. The buyer (us) is
// entitled to qty of security_ticker from the partner; the spec
// doesn't define a cross-bank security-delivery protocol so we just
// record the contract as exercised. No realized_gain row on the
// buyer side either — the seller (partner) handles their tax.

package service

import (
	"context"
	"fmt"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const externalOTCExerciseSagaType = "external_otc_exercise"

type externalOTCExercisePayload struct {
	ContractID          string `json:"contract_id"`
	ThreadID            string `json:"thread_id"`
	RemoteBankCode      string `json:"remote_bank_code"`
	RemoteThreadID      string `json:"remote_thread_id"`
	RemoteAccountRef    string `json:"remote_account_ref"`
	LocalAccountID      string `json:"local_account_id"`
	LocalAccountNumber  string `json:"local_account_number"`
	Quantity            int32  `json:"quantity"`
	StrikePrice         string `json:"strike_price"`
	TotalAmount         string `json:"total_amount"`
	Currency            string `json:"currency"`
	SenderRoutingNumber int    `json:"sender_routing_number"`

	// Banka-2 / si-tx-proto fields. When PartnerIsBanka2, exercise is a
	// buyer-coordinated multi-asset 2PC: we send the partner the exercise
	// NEW_TX (OPTION-account legs release the seller's shares + credit the
	// strike) and deliver the shares to our buyer locally.
	PartnerIsBanka2 bool   `json:"partner_is_banka2"`
	BuyerUserRef    string `json:"buyer_user_ref"`
	LocalUserID     string `json:"local_user_id"`
	LocalUserKind   string `json:"local_user_kind"`
	SecurityTicker  string `json:"security_ticker"`
}

var externalOTCExerciseNS = uuid.MustParse("c5e0f130-7e62-4f00-9c1d-1f24f2d8a402")

// externalOTCExerciseTxID derives the saga transaction_id from the
// contract id. Stable across retries.
func externalOTCExerciseTxID(contractID string) string {
	return uuid.NewSHA1(externalOTCExerciseNS, []byte(contractID)).String()
}

// exerciseExternalOutgoing drives the saga for an outgoing contract
// the local buyer wants to exercise.
func (s *Service) exerciseExternalOutgoing(ctx context.Context, contract *domain.ExternalOTCContract) (*domain.ExternalOTCContract, error) {
	if s.SagaOrch == nil || s.InterbankPayer == nil {
		return nil, apperr.FailedPrecondition("cross-bank infrastructure nije konfigurisana")
	}
	if contract.LocalRole != domain.ExternalOTCRoleBuyer {
		return nil, apperr.FailedPrecondition("samo kupac može iskoristiti opciju")
	}
	if contract.Status != domain.ExternalOTCContractActive {
		return nil, apperr.FailedPrecondition("ugovor nije aktivan")
	}

	// qty * strike in the contract currency.
	qty, _ := money.Parse(fmt.Sprintf("%d", contract.Quantity))
	strike, err := money.Parse(contract.StrikePrice)
	if err != nil {
		return nil, apperr.Internal("strike price unparseable", err)
	}
	total := money.Mul(qty, strike)

	txID := externalOTCExerciseTxID(contract.ID)

	payload := externalOTCExercisePayload{
		ContractID:          contract.ID,
		ThreadID:            contract.ThreadID,
		RemoteBankCode:      contract.RemoteBankCode,
		RemoteThreadID:      contract.RemoteThreadID,
		RemoteAccountRef:    contract.RemoteAccountRef,
		LocalAccountID:      contract.LocalAccountID,
		LocalAccountNumber:  contract.LocalAccountNumber,
		Quantity:            contract.Quantity,
		StrikePrice:         contract.StrikePrice,
		TotalAmount:         money.FormatAmount(total),
		Currency:            string(contract.Currency),
		SenderRoutingNumber: s.Cfg.OwnRoutingNumber,
		PartnerIsBanka2:     s.PartnerOTC != nil && s.PartnerOTC.SpeaksBanka2Dialect(ctx, contract.RemoteBankCode),
		BuyerUserRef:        contract.LocalUserID,
		LocalUserID:         contract.LocalUserID,
		LocalUserKind:       string(contract.LocalUserKind),
		SecurityTicker:      contract.SecurityTicker,
	}

	ctx = saga.FaultsFromMetadata(ctx, s.Cfg.SagaDebugFaultInjection)
	row, err := saga.Start(ctx, s.SagaOrch, saga.StartInput[externalOTCExercisePayload]{
		TransactionID: txID,
		SagaType:      externalOTCExerciseSagaType,
		InitialState:  payload,
		AttemptsMax:   8,
	})
	if err != nil {
		return nil, fmt.Errorf("external otc exercise saga: %w", err)
	}
	if row.Status != saga.StatusCompleted {
		if row.Status == saga.StatusRunning {
			return nil, status.Error(codes.Unavailable, "external otc exercise saga parked for retry")
		}
		return nil, apperr.Internal("external otc exercise saga did not complete", nil)
	}
	return s.Store.GetExternalOTCContract(ctx, contract.ID)
}

// registerExternalOTCExerciseSaga.
func registerExternalOTCExerciseSaga(reg *saga.Registry, svc *Service) {
	def := saga.Definition[externalOTCExercisePayload]{
		Type: externalOTCExerciseSagaType,
		Steps: []saga.Step[externalOTCExercisePayload]{
			{
				Name: "prepare_strike",
				Forward: func(ctx context.Context, sc *saga.Context[externalOTCExercisePayload]) error {
					_, err := svc.InterbankPayer.PreparePayment(ctx, PrepareInterbankInput{
						SenderRoutingNumber: sc.State.SenderRoutingNumber,
						TransactionID:       sc.TransactionID,
						LocalAccountNumber:  sc.State.LocalAccountNumber,
						RemoteAccountNumber: sc.State.RemoteAccountRef,
						Currency:            domain.Currency(sc.State.Currency),
						Amount:              sc.State.TotalAmount,
						Purpose:             "OTC izvršenje (eksterno) — ugovor " + sc.State.ContractID,
					})
					return err
				},
				Compensate: func(ctx context.Context, sc *saga.Context[externalOTCExercisePayload]) error {
					return svc.InterbankPayer.RollbackPayment(ctx,
						sc.State.SenderRoutingNumber, sc.TransactionID,
						"external otc exercise compensation")
				},
			},
			// Partner notification — there's no dedicated "Exercise" verb
			// on PartnerOTC yet (the outbound side covers Create/Counter/
			// Withdraw/Accept). The partner observes our exercise via
			// the 2PC commit that hits their bank's
			// InterbankProtocolService in step 3. BE-4c adds a dedicated
			// notification verb if partner banks need an earlier signal.
			//
			// For now, step 2 is a no-op that exists for shape parity
			// with the accept saga.
			{
				Name: "notify_partner_exercise",
				Forward: func(ctx context.Context, sc *saga.Context[externalOTCExercisePayload]) error {
					if !sc.State.PartnerIsBanka2 {
						// Native partners observe our exercise via the strike-leg
						// 2PC commit (step 3); no dedicated verb. No-op.
						return nil
					}
					// Banka-2 / si-tx-proto: WE coordinate. Send the exercise NEW_TX
					// (OPTION-account legs release the seller shares + credit the
					// strike) and drive it to COMMIT. A partner NO surfaces as a
					// permanent error -> compensate the local strike reservation.
					return svc.PartnerOTC.ExerciseOption(ctx, PartnerExerciseInput{
						RemoteBankCode: sc.State.RemoteBankCode,
						ContractID:     sc.State.RemoteThreadID,
						TransactionID:  sc.TransactionID,
						BuyerUserRef:   sc.State.BuyerUserRef,
						Quantity:       sc.State.Quantity,
						StrikeTotal:    sc.State.TotalAmount,
						Currency:       sc.State.Currency,
						Ticker:         sc.State.SecurityTicker,
					})
				},
				Compensate: nil,
			},
			{
				Name: "commit_strike",
				Forward: func(ctx context.Context, sc *saga.Context[externalOTCExercisePayload]) error {
					_, err := svc.InterbankPayer.CommitPayment(ctx,
						sc.State.SenderRoutingNumber, sc.TransactionID)
					return err
				},
				Compensate: nil,
			},
			{
				Name: "deliver_shares",
				Forward: func(ctx context.Context, sc *saga.Context[externalOTCExercisePayload]) error {
					if !sc.State.PartnerIsBanka2 || sc.State.SecurityTicker == "" {
						// Native exercise does not model local share delivery.
						return nil
					}
					// Banka-2 exercise grants the buyer qty shares (PERSON STOCK
					// +qty). Credit a local holding at strike cost. Idempotent via
					// the (user, security, account) upsert under the saga txid retry.
					sec, err := svc.Store.GetSecurityByTicker(ctx, sc.State.SecurityTicker, domain.SecurityStock)
					if err != nil {
						// Unknown ticker locally — strike already settled + contract
						// exercised; skip delivery rather than strand the saga.
						return nil
					}
					return svc.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
						_, aerr := svc.Store.ApplyBuyFill(ctx, tx, sc.State.LocalUserID,
							sc.State.LocalUserKind, sec.ID, sc.State.LocalAccountID,
							sc.State.Quantity, sc.State.StrikePrice)
						return aerr
					})
				},
				Compensate: nil,
			},
			{
				Name: "mark_exercised",
				Forward: func(ctx context.Context, sc *saga.Context[externalOTCExercisePayload]) error {
					opID := deriveExternalCommitOpID(sc.State.SenderRoutingNumber, sc.TransactionID)
					return svc.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
						_, merr := svc.Store.SetExternalOTCContractExercised(ctx, tx,
							sc.State.ContractID, opID, time.Now())
						return merr
					})
				},
				Compensate: nil,
			},
		},
	}
	saga.Register(reg, def)
}
