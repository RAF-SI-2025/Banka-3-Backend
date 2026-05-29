// OTC accept SAGA. Spec p.67-69, 79.
//
// Flow (intra-bank — c5 will swap the counterparty UUID for a
// (bank_code, account) tuple without reshaping these steps):
//
//   1. reserve_buyer_premium  — bank.ReserveFunds debits the buyer's
//      available_balance by the premium amount in the offer currency
//      (FX hop happens later at commit time).
//   2. reserve_seller_shares  — read-only assert that seller's holding
//      already reserves at least `qty`. No state change — the
//      reservation was bumped at offer-create time and the contract
//      will inherit it. No-op compensation.
//   3. transfer_premium       — bank.CommitReservedFunds finalises the
//      reservation, debiting buyer's balance and crediting seller's
//      account in the seller's account currency (FX leg goes via bank
//      house if currencies differ). FX commission rules:
//        - buyer is client  → commission ON  (client pays the FX fee)
//        - both supervisors → commission OFF (actuary path, spec p.55)
//   4. create_contract        — insert otc_contracts(status=active);
//      flip the live offer to `accepted`, prior `open` rows to
//      `superseded`. The seller's holding reserved_count stays as-is —
//      the offer's reservation rolls over to the contract.
//
// Idempotency
// ===========
// transaction_id is derived deterministically from the offer id so a
// retry of the same accept call finds the existing saga instead of
// starting a new one. Each bank-side step uses op_id = NewSHA1(tx_id,
// step_name), matching bank's (op_id, leg_index) unique index.

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

// otcAcceptSagaType is the saga registry key.
const otcAcceptSagaType = "otc_accept"

// otcAcceptSagaPayload is the persisted-state JSON for an in-flight
// accept saga.
type otcAcceptSagaPayload struct {
	ThreadID        string `json:"thread_id"`
	OfferID         string `json:"offer_id"`
	SellerHoldingID string `json:"seller_holding_id"`
	SecurityID      string `json:"security_id"`
	BuyerID         string `json:"buyer_id"`
	BuyerKind       string `json:"buyer_kind"`
	BuyerAccountID  string `json:"buyer_account_id"`
	SellerID        string `json:"seller_id"`
	SellerKind      string `json:"seller_kind"`
	SellerAccountID string `json:"seller_account_id"`
	Quantity        int32  `json:"quantity"`
	PricePerUnit    string `json:"price_per_unit"`
	Premium         string `json:"premium"`
	Currency        string `json:"currency"`
	SettlementDate  string `json:"settlement_date"`
	// IsActuary: true when both sides are supervisors; toggles the
	// bank's FX commission off on the commit leg.
	IsActuary bool `json:"is_actuary"`
}

// AcceptOTCOfferInput is the validated payload from the server.
type AcceptOTCOfferInput struct {
	ThreadID string
}

// AcceptOTCOfferResult bundles the contract + premium op_id for the
// FE / ops audit.
type AcceptOTCOfferResult struct {
	Contract    *domain.OTCContract
	PremiumOpID string
}

// AcceptOTCOffer drives the accept SAGA. Only the counterparty (the
// party that did NOT modify the open iteration last) may accept; this
// matches spec p.69's "Vaš odgovor" / "Čekanje odgovora" UX.
func (s *Service) AcceptOTCOffer(ctx context.Context, in AcceptOTCOfferInput) (*AcceptOTCOfferResult, error) {
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

	// Resolve the open iteration + validate the caller is the
	// counterparty.
	open, err := s.Store.GetOpenOTCOfferByThread(ctx, nil, in.ThreadID)
	if err != nil {
		return nil, err
	}
	if open.BuyerID != p.UserID && open.SellerID != p.UserID {
		return nil, apperr.PermissionDenied("niste strana u niti")
	}
	if open.ModifiedBy == p.UserID {
		return nil, apperr.FailedPrecondition("ne možete da prihvatite sopstvenu iteraciju")
	}

	// Deterministic transaction_id so a retry resumes rather than
	// re-runs.
	txID := otcAcceptTxID(open.ID)

	// Saga's IsActuary toggles the FX commission off on the commit leg.
	// Spec p.55 — actuary path zeroes FX commission. Mixed-role offers
	// are rejected earlier in CreateOTCOffer, so the buyer + seller
	// have the same kind here.
	isActuary := open.BuyerKind == domain.KindEmployee

	payload := otcAcceptSagaPayload{
		ThreadID:        open.ThreadID,
		OfferID:         open.ID,
		SellerHoldingID: open.SellerHoldingID,
		SecurityID:      open.SecurityID,
		BuyerID:         open.BuyerID,
		BuyerKind:       string(open.BuyerKind),
		BuyerAccountID:  open.BuyerAccountID,
		SellerID:        open.SellerID,
		SellerKind:      string(open.SellerKind),
		SellerAccountID: open.SellerAccountID,
		Quantity:        open.Quantity,
		PricePerUnit:    open.PricePerUnit,
		Premium:         open.Premium,
		Currency:        string(open.Currency),
		SettlementDate:  open.SettlementDate.Format("2006-01-02"),
		IsActuary:       isActuary,
	}

	ctx = saga.FaultsFromMetadata(ctx, s.Cfg.SagaDebugFaultInjection)
	row, err := saga.Start(ctx, s.SagaOrch, saga.StartInput[otcAcceptSagaPayload]{
		TransactionID: txID,
		SagaType:      otcAcceptSagaType,
		InitialState:  payload,
		AttemptsMax:   8,
	})
	if err != nil {
		return nil, fmt.Errorf("otc accept saga: %w", err)
	}
	if row.Status != saga.StatusCompleted {
		// Transient park: saga.Start suppresses the err, only signal
		// is row.Status=Running. Surface as Unavailable so the caller
		// polls/backoffs; recovery worker will drive it forward.
		// See [[reference_saga_park_status_mapping]] for the pattern.
		if row.Status == saga.StatusRunning {
			return nil, status.Error(codes.Unavailable, "otc accept saga parked for retry")
		}
		return nil, apperr.Internal("otc accept saga did not complete", nil)
	}

	contract, err := s.Store.GetOTCContractByThread(ctx, open.ThreadID)
	if err != nil {
		return nil, err
	}
	premiumOp := saga.DeriveOpID(txID, "transfer_premium")
	if s.OTCNotifier != nil {
		recipient, kind := otherContractParty(contract, p.UserID)
		s.OTCNotifier.OnOTCAccepted(ctx, contract, recipient, kind)
	}
	return &AcceptOTCOfferResult{Contract: contract, PremiumOpID: premiumOp}, nil
}

// registerOTCAcceptSaga registers the accept definition with the
// orchestrator's registry. Called from RegisterSagas at boot.
func registerOTCAcceptSaga(reg *saga.Registry, svc *Service) {
	def := saga.Definition[otcAcceptSagaPayload]{
		Type: otcAcceptSagaType,
		Steps: []saga.Step[otcAcceptSagaPayload]{
			// Step 1: reserve the premium on the buyer's account.
			{
				Name: "reserve_buyer_premium",
				Forward: func(ctx context.Context, sc *saga.Context[otcAcceptSagaPayload]) error {
					_, err := svc.Reservations.Reserve(ctx, ReserveInput{
						AccountID: sc.State.BuyerAccountID,
						Amount:    sc.State.Premium,
						Currency:  domain.Currency(sc.State.Currency),
						OpID:      sc.OpID,
						OpKind:    "otc_premium",
					})
					return err
				},
				Compensate: func(ctx context.Context, sc *saga.Context[otcAcceptSagaPayload]) error {
					_, err := svc.Reservations.Release(ctx, sc.OpID)
					return err
				},
			},
			// Step 2: verify the seller's holding still reserves at
			// least qty. Read-only — no state change here. The
			// reservation was bumped at offer-create time and the
			// contract will inherit it on step 4.
			{
				Name: "reserve_seller_shares",
				Forward: func(ctx context.Context, sc *saga.Context[otcAcceptSagaPayload]) error {
					h, err := svc.Store.GetHoldingByID(ctx, sc.State.SellerHoldingID)
					if err != nil {
						return err
					}
					if h.ReservedCount < sc.State.Quantity {
						return status.Error(codes.FailedPrecondition,
							"seller holding reservation insufficient")
					}
					return nil
				},
				Compensate: nil, // read-only
			},
			// Step 3: commit the premium (debit balance, credit seller).
			// FX leg goes via the bank house if currencies differ.
			{
				Name: "transfer_premium",
				Forward: func(ctx context.Context, sc *saga.Context[otcAcceptSagaPayload]) error {
					reserveOp := saga.DeriveOpID(sc.TransactionID, "reserve_buyer_premium")
					_, err := svc.Reservations.Commit(ctx, CommitInput{
						OpID:          reserveOp,
						DestAccountID: sc.State.SellerAccountID,
						DestAmount:    sc.State.Premium,
						DestCurrency:  domain.Currency(sc.State.Currency),
						IsActuary:     sc.State.IsActuary,
						Purpose:       "OTC premija — nit " + sc.State.ThreadID,
					})
					return err
				},
				// Best-effort compensation: re-release. Once a commit
				// has succeeded, the bank's reservation row is
				// `committed` and the release call is a no-op (returns
				// released=false). For an in-flight commit that the
				// orchestrator marks failed but actually completed at
				// the bank, the reservation is committed and the
				// release is harmless; the saga ends in `failed` and
				// the create_contract step's compensation runs (no
				// contract was created so it's a no-op too).
				Compensate: func(ctx context.Context, sc *saga.Context[otcAcceptSagaPayload]) error {
					reserveOp := saga.DeriveOpID(sc.TransactionID, "reserve_buyer_premium")
					_, err := svc.Reservations.Release(ctx, reserveOp)
					return err
				},
			},
			// Step 4: create the contract; flip the offer to accepted.
			{
				Name: "create_contract",
				Forward: func(ctx context.Context, sc *saga.Context[otcAcceptSagaPayload]) error {
					premiumOp := saga.DeriveOpID(sc.TransactionID, "transfer_premium")
					settlement, err := time.Parse("2006-01-02", sc.State.SettlementDate)
					if err != nil {
						return status.Error(codes.InvalidArgument, "bad settlement_date")
					}
					return svc.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
						_, err := svc.Store.InsertOTCContract(ctx, tx, &domain.OTCContract{
							ThreadID:        sc.State.ThreadID,
							SecurityID:      sc.State.SecurityID,
							SellerHoldingID: sc.State.SellerHoldingID,
							BuyerID:         sc.State.BuyerID,
							BuyerKind:       domain.UserKind(sc.State.BuyerKind),
							BuyerAccountID:  sc.State.BuyerAccountID,
							SellerID:        sc.State.SellerID,
							SellerKind:      domain.UserKind(sc.State.SellerKind),
							SellerAccountID: sc.State.SellerAccountID,
							Quantity:        sc.State.Quantity,
							StrikePrice:     sc.State.PricePerUnit,
							PremiumPaid:     sc.State.Premium,
							Currency:        domain.Currency(sc.State.Currency),
							SettlementDate:  settlement,
							PremiumOpID:     premiumOp,
							Status:          domain.OTCContractActive,
						})
						if err != nil {
							return err
						}
						return svc.Store.MarkAllOTCOffersAcceptedInThread(ctx, tx, sc.State.ThreadID, sc.State.OfferID)
					})
				},
				Compensate: func(ctx context.Context, sc *saga.Context[otcAcceptSagaPayload]) error {
					return svc.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
						if err := svc.Store.DeleteOTCContractByThread(ctx, tx, sc.State.ThreadID); err != nil {
							return err
						}
						// Flip the offer back to `open` so the buyer
						// can retry or withdraw.
						_, err := svc.Store.MarkOTCOfferStatus(ctx, tx, sc.State.OfferID, domain.OTCStatusOpen)
						return err
					})
				},
			},
		},
	}
	saga.Register(reg, def)
}

// otcAcceptTxID derives a deterministic transaction_id from the offer
// id. Same offer = same saga; retries resume.
func otcAcceptTxID(offerID string) string {
	return uuid.NewSHA1(otcAcceptNS, []byte(offerID)).String()
}

var otcAcceptNS = uuid.MustParse("c4ac6f15-cafe-4f6f-9d22-a0d4b9d8f7c1")

// otherContractParty mirrors otherParty for contracts.
func otherContractParty(c *domain.OTCContract, me string) (string, domain.UserKind) {
	if c.BuyerID == me {
		return c.SellerID, c.SellerKind
	}
	return c.BuyerID, c.BuyerKind
}
