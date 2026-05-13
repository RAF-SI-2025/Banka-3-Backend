// OTC exercise SAGA (c4-PR2, OTC-4). Spec p.80 intra-bank flow:
//
//   1. reserve_buyer_strike — bank.ReserveFunds on buyer's account for
//      qty × strike, in the contract currency.
//   2. verify_seller_shares — read-only assert that the seller's
//      holding still reserves at least `qty` (the contract's
//      reservation, inherited from the offer at accept time).
//   3. transfer_strike      — bank.CommitReservedFunds finalises the
//      reservation, debiting buyer's balance and crediting seller's
//      account in seller's account currency. FX commission off when
//      both parties are supervisors (actuary path, spec p.55).
//   4. transfer_shares      — in one trading-side tx: decrement
//      seller's holding quantity + reserved_count by qty; upsert
//      buyer's holding at strike cost-basis (weighted-avg with
//      existing position if any); insert a realized_gains row for
//      the SELLER (spec p.62 — "porez na kapitalnu dobit prilikom
//      prodaje akcija (preko berze i OTC trgovinom)"). Compensation
//      reverses the holding mutations and deletes the gain row.
//   5. finalize             — flip the contract to `exercised`,
//      stamping exercised_op_id (= strike-leg op_id) and
//      exercise_saga_id (= transaction_id).
//
// Idempotency
// ===========
// transaction_id is derived from the contract id; a retry of the same
// exercise call resumes the parked saga. Each bank-side step uses
// op_id = NewSHA1(tx_id, step_name).
//
// Realized-gain math
// ==================
// Seller's `gain_native = qty × (strike − seller_weighted_avg)` in the
// contract currency. `gain_rsd` is converted via the rate provider's
// ASK with no commission (spec p.62 — taxation is currency-neutral and
// fee-free).

package service

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const otcExerciseSagaType = "otc_exercise"

type otcExerciseSagaPayload struct {
	ContractID      string `json:"contract_id"`
	ThreadID        string `json:"thread_id"`
	SecurityID      string `json:"security_id"`
	SellerHoldingID string `json:"seller_holding_id"`
	BuyerID         string `json:"buyer_id"`
	BuyerKind       string `json:"buyer_kind"`
	BuyerAccountID  string `json:"buyer_account_id"`
	SellerID        string `json:"seller_id"`
	SellerKind      string `json:"seller_kind"`
	SellerAccountID string `json:"seller_account_id"`
	Quantity        int32  `json:"quantity"`
	StrikePrice     string `json:"strike_price"`
	TotalAmount     string `json:"total_amount"` // qty * strike, in contract currency
	Currency        string `json:"currency"`
	IsActuary       bool   `json:"is_actuary"`
	// Carried across the steps so the realized_gain insert sees the
	// seller's pre-fill cost basis. Stamped by step 4's forward branch
	// after the holding update returns it.
	SellerCostBasis    string `json:"seller_cost_basis"`
	RealizedGainID     string `json:"realized_gain_id"`
	RealizedGainNative string `json:"realized_gain_native"`
	RealizedGainRSD    string `json:"realized_gain_rsd"`
}

// ExerciseOTCContractInput is the validated payload.
type ExerciseOTCContractInput struct {
	ContractID string
}

// ExerciseOTCContractResult is what the server returns.
type ExerciseOTCContractResult struct {
	Contract                 *domain.OTCContract
	StrikeOpID               string
	SellerRealizedGainNative string
	SellerRealizedGainRSD    string
}

// ExerciseOTCContract kicks off the exercise saga for an active
// contract. Only the buyer (or admin) may exercise; spec p.80 implies
// this — the buyer holds the option, the seller is locked into delivery.
func (s *Service) ExerciseOTCContract(ctx context.Context, in ExerciseOTCContractInput) (*ExerciseOTCContractResult, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	if s.SagaOrch == nil || s.Reservations == nil {
		return nil, apperr.Internal("saga orchestrator or bank reservations not wired", nil)
	}

	c, err := s.Store.GetOTCContract(ctx, in.ContractID)
	if err != nil {
		return nil, err
	}
	if c.BuyerID != p.UserID {
		// Admin may force-exercise for ops; the seller cannot.
		if !permissions.Has(p.Permissions, permissions.Admin) {
			return nil, apperr.PermissionDenied("samo kupac može da iskoristi ugovor")
		}
	}
	if c.Status != domain.OTCContractActive {
		return nil, apperr.FailedPrecondition("ugovor nije aktivan")
	}
	if !c.SettlementDate.After(s.now()) {
		return nil, apperr.FailedPrecondition("ugovor je istekao")
	}

	// Total cost = qty * strike, in contract currency.
	q := new(big.Rat).SetInt64(int64(c.Quantity))
	strike, err := money.Parse(c.StrikePrice)
	if err != nil {
		return nil, apperr.Internal("strike parse", err)
	}
	total := money.Mul(q, strike)

	payload := otcExerciseSagaPayload{
		ContractID:      c.ID,
		ThreadID:        c.ThreadID,
		SecurityID:      c.SecurityID,
		SellerHoldingID: c.SellerHoldingID,
		BuyerID:         c.BuyerID,
		BuyerKind:       string(c.BuyerKind),
		BuyerAccountID:  c.BuyerAccountID,
		SellerID:        c.SellerID,
		SellerKind:      string(c.SellerKind),
		SellerAccountID: c.SellerAccountID,
		Quantity:        c.Quantity,
		StrikePrice:     c.StrikePrice,
		TotalAmount:     money.FormatAmount(total),
		Currency:        string(c.Currency),
		IsActuary:       c.BuyerKind == domain.KindEmployee,
	}

	txID := otcExerciseTxID(c.ID)
	ctx = saga.FaultsFromMetadata(ctx, s.Cfg.SagaDebugFaultInjection)
	row, err := saga.Start(ctx, s.SagaOrch, saga.StartInput[otcExerciseSagaPayload]{
		TransactionID: txID,
		SagaType:      otcExerciseSagaType,
		InitialState:  payload,
		AttemptsMax:   8,
	})
	if err != nil {
		// Surface the saga's own LastError to the caller so the
		// FE/test sees the originating step's reason
		// (e.g. "nedovoljno sredstava na računu"), not a generic
		// "internal error". The saga rolled forward+compensated
		// already; this is a business-rule failure, not a system
		// fault, so map it to FailedPrecondition.
		return nil, apperr.FailedPrecondition(sagaFailureMessage(row, err))
	}
	if row.Status != saga.StatusCompleted {
		return nil, apperr.FailedPrecondition(sagaFailureMessage(row, nil))
	}

	// Reload + return final state.
	finalRow, err := s.SagaStore.Get(ctx, txID)
	if err != nil {
		return nil, err
	}
	var finalPayload otcExerciseSagaPayload
	if finalRow != nil && len(finalRow.State) > 0 {
		_ = json.Unmarshal(finalRow.State, &finalPayload)
	}
	updated, err := s.Store.GetOTCContract(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	strikeOp := saga.DeriveOpID(txID, "transfer_strike")
	return &ExerciseOTCContractResult{
		Contract:                 updated,
		StrikeOpID:               strikeOp,
		SellerRealizedGainNative: finalPayload.RealizedGainNative,
		SellerRealizedGainRSD:    finalPayload.RealizedGainRSD,
	}, nil
}

// registerOTCExerciseSaga registers the exercise definition with the
// orchestrator's registry.
func registerOTCExerciseSaga(reg *saga.Registry, svc *Service) {
	def := saga.Definition[otcExerciseSagaPayload]{
		Type: otcExerciseSagaType,
		Steps: []saga.Step[otcExerciseSagaPayload]{
			{
				Name: "reserve_buyer_strike",
				Forward: func(ctx context.Context, sc *saga.Context[otcExerciseSagaPayload]) error {
					_, err := svc.Reservations.Reserve(ctx, ReserveInput{
						AccountID: sc.State.BuyerAccountID,
						Amount:    sc.State.TotalAmount,
						Currency:  domain.Currency(sc.State.Currency),
						OpID:      sc.OpID,
						OpKind:    "otc_exercise",
					})
					return err
				},
				Compensate: func(ctx context.Context, sc *saga.Context[otcExerciseSagaPayload]) error {
					_, err := svc.Reservations.Release(ctx, sc.OpID)
					return err
				},
			},
			{
				Name: "verify_seller_shares",
				Forward: func(ctx context.Context, sc *saga.Context[otcExerciseSagaPayload]) error {
					h, err := svc.Store.GetHoldingByID(ctx, sc.State.SellerHoldingID)
					if err != nil {
						return err
					}
					if h.Quantity < sc.State.Quantity || h.ReservedCount < sc.State.Quantity {
						return status.Error(codes.FailedPrecondition,
							"seller holding no longer covers contract quantity")
					}
					return nil
				},
				Compensate: nil, // read-only
			},
			{
				Name: "transfer_strike",
				Forward: func(ctx context.Context, sc *saga.Context[otcExerciseSagaPayload]) error {
					reserveOp := saga.DeriveOpID(sc.TransactionID, "reserve_buyer_strike")
					_, err := svc.Reservations.Commit(ctx, CommitInput{
						OpID:          reserveOp,
						DestAccountID: sc.State.SellerAccountID,
						DestAmount:    sc.State.TotalAmount,
						DestCurrency:  domain.Currency(sc.State.Currency),
						IsActuary:     sc.State.IsActuary,
						Purpose:       "OTC izvršenje — ugovor " + sc.State.ContractID,
					})
					return err
				},
				Compensate: func(ctx context.Context, sc *saga.Context[otcExerciseSagaPayload]) error {
					// Forward Commit already moved real money seller-ward
					// (the reservation row is in 'committed' state).
					// bank.ReleaseFunds only handles the 'held' case, so
					// the compensation has to issue a reverse transfer
					// to return the strike amount to the buyer.
					reverseOp := saga.DeriveOpID(sc.TransactionID, "transfer_strike_reverse")
					_, err := svc.Reservations.Transfer(ctx, TransferInput{
						FromAccountID: sc.State.SellerAccountID,
						ToAccountID:   sc.State.BuyerAccountID,
						Amount:        sc.State.TotalAmount,
						OpID:          reverseOp,
						OpKind:        "otc_exercise",
						IsActuary:     sc.State.IsActuary,
						Purpose:       "Rollback OTC izvršenja — ugovor " + sc.State.ContractID,
					})
					return err
				},
			},
			{
				Name: "transfer_shares",
				Forward: func(ctx context.Context, sc *saga.Context[otcExerciseSagaPayload]) error {
					return svc.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
						// 1. Sell side: capture pre-fill cost basis,
						//    then decrement quantity AND reserved_count.
						sellerHolding, err := svc.Store.GetHoldingByID(ctx, sc.State.SellerHoldingID)
						if err != nil {
							return err
						}
						sc.State.SellerCostBasis = sellerHolding.WeightedAvgPrice
						avg, _, err := svc.Store.ApplySellFill(ctx, tx,
							sellerHolding.UserID, string(sellerHolding.UserKind),
							sellerHolding.SecurityID, sellerHolding.AccountID,
							sc.State.Quantity,
						)
						if err != nil {
							return err
						}
						if _, err := svc.Store.DecrementReservedHolding(ctx, tx, sc.State.SellerHoldingID, sc.State.Quantity); err != nil {
							return err
						}

						// 2. Buy side: upsert at strike (the buyer's
						//    cost basis on the underlying is what they
						//    paid — strike — not the listing price).
						buyerHolding, err := svc.Store.ApplyBuyFill(ctx, tx,
							sc.State.BuyerID, sc.State.BuyerKind,
							sc.State.SecurityID, sc.State.BuyerAccountID,
							sc.State.Quantity, sc.State.StrikePrice,
						)
						if err != nil {
							return err
						}
						_ = buyerHolding

						// 3. Seller realized gain (spec p.62 EDGE-2).
						strike, _ := money.Parse(sc.State.StrikePrice)
						cost, _ := money.Parse(avg)
						q := new(big.Rat).SetInt64(int64(sc.State.Quantity))
						gainNative := money.Mul(q, money.Sub(strike, cost))
						costBasis := money.Mul(q, cost)
						proceeds := money.Mul(q, strike)

						cur := domain.Currency(sc.State.Currency)
						gainRSD := new(big.Rat).Set(gainNative)
						if cur != domain.CurrencyRSD && svc.Rates != nil {
							if _, ask, err := svc.Rates.Quote(ctx, cur, domain.CurrencyRSD); err == nil {
								if r, perr := money.Parse(ask); perr == nil {
									gainRSD = money.Mul(gainNative, r)
								}
							}
						}

						rg, err := svc.Store.InsertRealizedGain(ctx, tx, &domain.RealizedGain{
							UserID:       sellerHolding.UserID,
							UserKind:     sellerHolding.UserKind,
							SecurityID:   sc.State.SecurityID,
							AccountID:    sc.State.SellerAccountID,
							Quantity:     sc.State.Quantity,
							CostBasisAmt: money.FormatAmount(costBasis),
							ProceedsAmt:  money.FormatAmount(proceeds),
							Currency:     cur,
							GainNative:   money.FormatAmount(gainNative),
							GainRSD:      money.FormatAmount(gainRSD),
						})
						if err != nil {
							return err
						}
						sc.State.RealizedGainID = rg.ID
						sc.State.RealizedGainNative = rg.GainNative
						sc.State.RealizedGainRSD = rg.GainRSD
						return nil
					})
				},
				Compensate: func(ctx context.Context, sc *saga.Context[otcExerciseSagaPayload]) error {
					return svc.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
						// Reverse the seller decrement: add back qty.
						sellerHolding, err := svc.Store.GetHoldingByID(ctx, sc.State.SellerHoldingID)
						if err != nil {
							return err
						}
						if _, err := svc.Store.ApplyBuyFill(ctx, tx,
							sellerHolding.UserID, string(sellerHolding.UserKind),
							sellerHolding.SecurityID, sellerHolding.AccountID,
							sc.State.Quantity, sc.State.SellerCostBasis,
						); err != nil {
							return err
						}
						// Re-arm the reservation that we released along
						// with quantity.
						if _, err := svc.Store.IncrementReservedHolding(ctx, tx, sc.State.SellerHoldingID, sc.State.Quantity); err != nil {
							return err
						}
						// Reverse buyer's buy-fill — decrement by qty.
						// We don't track the buyer's holding_id in the
						// payload, so rely on the (user, security,
						// account) tuple. If qty hits zero the row
						// stays (UNIQUE keeps the audit row).
						if _, _, err := svc.Store.ApplySellFill(ctx, tx,
							sc.State.BuyerID, sc.State.BuyerKind,
							sc.State.SecurityID, sc.State.BuyerAccountID,
							sc.State.Quantity,
						); err != nil {
							return err
						}
						// Delete the realized_gain row if we have its id.
						if sc.State.RealizedGainID != "" {
							if _, err := tx.Exec(ctx, `delete from "trading".realized_gains where id = $1`, sc.State.RealizedGainID); err != nil {
								return err
							}
							sc.State.RealizedGainID = ""
							sc.State.RealizedGainNative = ""
							sc.State.RealizedGainRSD = ""
						}
						return nil
					})
				},
			},
			{
				Name: "finalize",
				Forward: func(ctx context.Context, sc *saga.Context[otcExerciseSagaPayload]) error {
					strikeOp := saga.DeriveOpID(sc.TransactionID, "transfer_strike")
					now := time.Now()
					return svc.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
						_, err := svc.Store.MarkOTCContractStatus(ctx, tx,
							sc.State.ContractID,
							domain.OTCContractExercised,
							strikeOp, sc.TransactionID, &now,
						)
						return err
					})
				},
				Compensate: nil, // last step
			},
		},
	}
	saga.Register(reg, def)
}

// otcExerciseTxID derives a deterministic transaction_id from the
// contract id.
func otcExerciseTxID(contractID string) string {
	return uuid.NewSHA1(otcExerciseNS, []byte(contractID)).String()
}

var otcExerciseNS = uuid.MustParse("c4ec6f15-cafe-4f6f-9d22-b0d4b9d8f7c2")

// sagaFailureMessage picks the most useful Serbian-or-other-source
// failure copy out of a finished saga: the row's LastError when set
// (already stripped of any "rpc error: code = X desc =" envelope), or
// the raw err string. Falls back to a fixed Serbian sentinel if both
// are empty so the caller always has something to render.
func sagaFailureMessage(row *saga.Row, err error) string {
	if row != nil && row.LastError != "" {
		return stripRPCEnvelope(row.LastError)
	}
	if err != nil {
		return stripRPCEnvelope(err.Error())
	}
	return "OTC izvršenje nije uspelo"
}

// stripRPCEnvelope reduces "rpc error: code = X desc = Y" to "Y" so the
// FE/test sees the underlying Serbian copy rather than the gRPC framing.
func stripRPCEnvelope(s string) string {
	const marker = "desc = "
	if i := strings.LastIndex(s, marker); i >= 0 {
		return s[i+len(marker):]
	}
	return s
}
