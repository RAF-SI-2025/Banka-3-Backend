// Cross-bank payment SAGA (celina 5 — spec p.77+).
//
// Drives a user-initiated cross-bank cash payment over our 2PC primitive.
// We are always the sending bank; the partner is the receiving bank.
//
// Steps:
//   1. prepare_local   — bank.PreparePayment(direction=OUTBOUND) reserves
//      the source account's funds. Compensate: bank.RollbackPayment
//      releases the reservation.
//   2. prepare_partner — partner.NEW_TX over HTTP (native or banka2). A
//      NO vote or HTTP error surfaces as a permanent error and
//      compensates step 1.
//   3. commit_partner  — partner.COMMIT_TX. From here the partner has
//      credited their user. No reverse-direction compensation defined.
//   4. commit_local    — bank.CommitPayment finalises our side (moves
//      reserved funds from the user → bank.system_<currency>).
//
// Same shape as external_otc_accept_saga.go — prepare both sides, then
// commit both. Commit order is partner-first by spec convention: once
// they ack the commit, our side has to follow through (saga retries
// step 4 until it succeeds; parking handles transient bank-side errors).

package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const crossBankPaymentSagaType = "cross_bank_payment"

type crossBankPaymentPayload struct {
	UserID              string `json:"user_id"`
	UserKind            string `json:"user_kind"`
	SourceAccountID     string `json:"source_account_id"`
	SourceAccountNumber string `json:"source_account_number"`
	RemoteBankCode      string `json:"remote_bank_code"`
	RemoteAccountNumber string `json:"remote_account_number"`
	Currency            string `json:"currency"`
	Amount              string `json:"amount"`
	Purpose             string `json:"purpose"`
	SenderRoutingNumber int    `json:"sender_routing_number"`
}

var crossBankPaymentNS = uuid.MustParse("c5b00100-9e1a-4f00-b00d-1a24f2d8a402")

// crossBankPaymentTxID derives a deterministic saga transaction_id from
// a request-scoped key (user id + idempotency key + source account).
// Stable across retries.
func crossBankPaymentTxID(userID, idempotencyKey, srcAcct string) string {
	return uuid.NewSHA1(crossBankPaymentNS,
		[]byte(fmt.Sprintf("%s:%s:%s", userID, idempotencyKey, srcAcct))).String()
}

// registerCrossBankPaymentSaga registers the saga at boot.
func registerCrossBankPaymentSaga(reg *saga.Registry, svc *Service) {
	def := saga.Definition[crossBankPaymentPayload]{
		Type: crossBankPaymentSagaType,
		Steps: []saga.Step[crossBankPaymentPayload]{
			{
				Name: "prepare_local",
				Forward: func(ctx context.Context, sc *saga.Context[crossBankPaymentPayload]) error {
					sc.Log.DebugContext(ctx, "cross-bank payment: preparing local leg",
						"remote_bank_code", sc.State.RemoteBankCode,
						"amount", sc.State.Amount, "currency", sc.State.Currency)
					_, err := svc.InterbankPayer.PreparePayment(ctx, PrepareInterbankInput{
						SenderRoutingNumber: sc.State.SenderRoutingNumber,
						TransactionID:       sc.TransactionID,
						LocalAccountNumber:  sc.State.SourceAccountNumber,
						RemoteAccountNumber: sc.State.RemoteAccountNumber,
						Currency:            domain.Currency(sc.State.Currency),
						Amount:              sc.State.Amount,
						Purpose:             sc.State.Purpose,
					})
					if err != nil {
						sc.Log.ErrorContext(ctx, "cross-bank payment: local prepare failed",
							"err", err, "remote_bank_code", sc.State.RemoteBankCode,
							"amount", sc.State.Amount, "currency", sc.State.Currency)
					}
					return err
				},
				Compensate: func(ctx context.Context, sc *saga.Context[crossBankPaymentPayload]) error {
					if err := svc.InterbankPayer.RollbackPayment(ctx,
						sc.State.SenderRoutingNumber, sc.TransactionID,
						"cross-bank payment local rollback"); err != nil {
						sc.Log.ErrorContext(ctx, "cross-bank payment: local rollback compensation failed",
							"err", err, "remote_bank_code", sc.State.RemoteBankCode)
						return err
					}
					sc.Log.InfoContext(ctx, "cross-bank payment: local reservation rolled back",
						"remote_bank_code", sc.State.RemoteBankCode)
					return nil
				},
			},
			{
				Name: "prepare_partner",
				Forward: func(ctx context.Context, sc *saga.Context[crossBankPaymentPayload]) error {
					res, err := svc.PartnerPayer.PreparePayment(ctx, PartnerPaymentInput{
						RemoteBankCode:      sc.State.RemoteBankCode,
						TransactionID:       sc.TransactionID,
						LocalAccountNumber:  sc.State.SourceAccountNumber,
						RemoteAccountNumber: sc.State.RemoteAccountNumber,
						Currency:            sc.State.Currency,
						Amount:              sc.State.Amount,
						Purpose:             sc.State.Purpose,
					})
					if err != nil {
						sc.Log.ErrorContext(ctx, "cross-bank payment: partner prepare failed",
							"err", err, "remote_bank_code", sc.State.RemoteBankCode,
							"amount", sc.State.Amount, "currency", sc.State.Currency)
						return err
					}
					if !res.Accepted {
						reason := "partner refused"
						if len(res.NoReasons) > 0 {
							reason = "partner refused: " + strings.Join(res.NoReasons, ",")
						}
						sc.Log.WarnContext(ctx, "cross-bank payment: partner voted no",
							"remote_bank_code", sc.State.RemoteBankCode, "reason", reason)
						// Wrap as InvalidArgument so the saga marks this as
						// a permanent failure (not transient retry).
						return status.Error(codes.InvalidArgument, reason)
					}
					sc.Log.InfoContext(ctx, "cross-bank payment: partner prepared",
						"remote_bank_code", sc.State.RemoteBankCode,
						"amount", sc.State.Amount, "currency", sc.State.Currency)
					return nil
				},
				Compensate: func(ctx context.Context, sc *saga.Context[crossBankPaymentPayload]) error {
					// Best-effort — if the partner never recorded the
					// prepare row, this 404s and we swallow it.
					if err := svc.PartnerPayer.RollbackPayment(ctx,
						sc.State.RemoteBankCode, sc.TransactionID,
						"cross-bank payment partner rollback"); err != nil {
						sc.Log.ErrorContext(ctx, "cross-bank payment: partner rollback compensation failed",
							"err", err, "remote_bank_code", sc.State.RemoteBankCode)
						return err
					}
					return nil
				},
			},
			{
				Name: "commit_partner",
				Forward: func(ctx context.Context, sc *saga.Context[crossBankPaymentPayload]) error {
					if err := svc.PartnerPayer.CommitPayment(ctx,
						sc.State.RemoteBankCode, sc.TransactionID); err != nil {
						sc.Log.ErrorContext(ctx, "cross-bank payment: partner commit failed",
							"err", err, "remote_bank_code", sc.State.RemoteBankCode)
						return err
					}
					sc.Log.InfoContext(ctx, "cross-bank payment: partner committed",
						"remote_bank_code", sc.State.RemoteBankCode)
					return nil
				},
				// Past the point of no return for the partner; no
				// compensation defined. A failure here parks the saga
				// for retry — the partner's commit is idempotent so
				// re-fires are safe.
				Compensate: nil,
			},
			{
				Name: "commit_local",
				Forward: func(ctx context.Context, sc *saga.Context[crossBankPaymentPayload]) error {
					_, err := svc.InterbankPayer.CommitPayment(ctx,
						sc.State.SenderRoutingNumber, sc.TransactionID)
					if err != nil {
						sc.Log.ErrorContext(ctx, "cross-bank payment: local commit failed",
							"err", err, "remote_bank_code", sc.State.RemoteBankCode)
						return err
					}
					sc.Log.InfoContext(ctx, "cross-bank payment: local leg committed",
						"remote_bank_code", sc.State.RemoteBankCode,
						"amount", sc.State.Amount, "currency", sc.State.Currency)
					return nil
				},
				Compensate: nil,
			},
		},
	}
	saga.Register(reg, def)
}
