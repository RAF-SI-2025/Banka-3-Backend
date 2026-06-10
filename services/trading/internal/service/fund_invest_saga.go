// Fund-invest SAGA (spec p.71-75).
//
// Three forward steps:
//
// The investor commits a figure in RSD — the fund's accounting unit
// (spec p.71 ClientFundTransaction.Iznos "u RSD", minimumContribution
// in RSD). The source-currency debit is derived from it, never the
// other way round; this keeps the minimumContribution gate exact and
// mirrors WithdrawFromFund.
//
//   1. reserve_source       — bank.ReserveFunds debits available_balance
//      on the investor's source account by source_amount (the committed
//      RSD converted into the source account's currency, commission
//      grossed in for clients). FX hop happens at commit time.
//   2. transfer_to_fund     — bank.CommitReservedFunds finalises the
//      reservation, debiting balance on the source and crediting the
//      full committed RSD (amount_rsd) to the fund's RSD account. The
//      commission is the src→RSD gap baked into source_amount:
//        - client investor  → commission ON  (debit grossed by 1/(1-c))
//        - supervisor (bank)→ commission OFF (actuary path, spec p.55)
//   3. record_position      — upsert client_fund_positions; bump fund
//      total_units; mark the audit row completed.
//
// Idempotency
// ===========
// transaction_id is derived from the audit row's id (one row per
// invest request, generated foreground) so a retry of the same kick-
// off resumes the existing saga instead of starting a new one. Each
// bank-side step uses op_id = NewSHA1(tx_id, step_name), matching
// bank's (op_id, leg_index) unique index.
//
// Unit-pricing
// ============
// The number of units minted is captured at saga kick-off
// (`unit_price = total_value_rsd / total_units` at request time;
// first invest defaults unit_price = 1). The payload carries the
// units_delta forward so the record_position step doesn't have to
// re-quote rates from inside a saga retry.

package service

import (
	"context"
	"fmt"
	"math/big"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const fundInvestSagaType = "fund_invest"

// fundInvestPayload is the persisted-state JSON of an in-flight invest.
type fundInvestPayload struct {
	TransactionRowID    string `json:"transaction_row_id"`
	FundID              string `json:"fund_id"`
	FundBankAccountID   string `json:"fund_bank_account_id"`
	ClientID            string `json:"client_id"`
	InitiatorEmployeeID string `json:"initiator_employee_id"`
	SourceAccountID     string `json:"source_account_id"`
	SourceCurrency      string `json:"source_currency"`
	SourceAmount        string `json:"source_amount"` // in source currency, commission incl.
	AmountRSD           string `json:"amount_rsd"`    // the committed RSD (fund credit)
	UnitsDelta          string `json:"units_delta"`
	IsActuary           bool   `json:"is_actuary"`
}

// InvestInFundInput is the validated payload.
type InvestInFundInput struct {
	FundID           string
	AmountRSD        string // RSD — the fund's accounting unit (spec p.71)
	SourceAccountID  string
	OnBehalfClientID string // sentinel for "in name of bank"; empty for self
}

// InvestInFundResult is what the FE sees after kick-off.
type InvestInFundResult struct {
	Transaction *domain.FundTransaction
	SagaID      string
	Pending     bool // always false for invest (synchronous on the saga's success path)
}

// InvestInFund prepares the invest audit row + saga payload, persists
// both, and runs the saga forward through saga.Start. The audit row
// stays `pending` until the saga's record_position step lands.
func (s *Service) InvestInFund(ctx context.Context, in InvestInFundInput) (*InvestInFundResult, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if s.SagaOrch == nil || s.Reservations == nil {
		return nil, apperr.Internal("saga orchestrator or bank reservations not wired", nil)
	}

	investor, isInNameOfBank, err := resolveFundInvestor(p, in.OnBehalfClientID)
	if err != nil {
		return nil, err
	}
	// Permission gate: clients need funds.invest.client; supervisors
	// need funds.manage.supervisor when acting "in name of bank".
	if isInNameOfBank {
		if err := s.requireFundsManage(p); err != nil {
			return nil, err
		}
	} else {
		if err := s.requireFundsInvestClient(p); err != nil {
			return nil, err
		}
	}

	amountRSD, err := money.Parse(in.AmountRSD)
	if err != nil || !money.IsPositive(amountRSD) {
		return nil, apperr.Validation("amount nije validan iznos")
	}

	f, err := s.Store.GetFund(ctx, in.FundID)
	if err != nil {
		return nil, err
	}
	if f.Status != domain.FundActive {
		return nil, apperr.FailedPrecondition("fond nije aktivan")
	}

	// minimum_contribution gate on the committed RSD amount. Exact —
	// the user states the figure in the fund's own currency, so no
	// FX conversion can blur the threshold (spec p.74 "proveriti
	// constraint za minimumContribution").
	min, err := money.Parse(f.MinimumContribution)
	if err == nil && min.Sign() > 0 && amountRSD.Cmp(min) < 0 {
		return nil, apperr.FailedPrecondition("iznos je ispod minimalnog uloga fonda")
	}

	// Source account currency lookup, then convert the committed RSD
	// → the source account's currency for the debit. Commission is
	// added on top for clients (the fund still receives the full
	// committed RSD); none for the bank/actuary path (spec p.55).
	// RSD source passes through unchanged.
	srcCurrency, _, err := s.Reservations.AccountAvailable(ctx, in.SourceAccountID)
	if err != nil {
		return nil, fmt.Errorf("bank.GetAccount(source): %w", err)
	}
	sourceAmount, err := s.convertFromRSDForFundInvest(ctx, srcCurrency, amountRSD, !isInNameOfBank)
	if err != nil {
		return nil, err
	}

	// Unit pricing snapshot. Run inside a fresh decoration to capture
	// the fund's *current* value before this investment. EDGE-10: first
	// invest mints at unit_price = 1.
	dec := s.decorateFund(ctx, f)
	unitPrice, err := money.Parse(dec.UnitPriceRSD)
	if err != nil || !money.IsPositive(unitPrice) {
		unitPrice = money.MustParse("1")
	}
	unitsDelta, err := money.Div(amountRSD, unitPrice)
	if err != nil {
		return nil, apperr.Internal("unit math failed", err)
	}

	initiator := ""
	if isInNameOfBank {
		initiator = p.UserID
	}

	// Persist the audit row and saga state together, then kick off.
	var auditID, txID string
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		row, err := s.Store.InsertFundTransaction(ctx, tx, &domain.FundTransaction{
			FundID:                f.ID,
			ClientID:              investor,
			InitiatorEmployeeID:   initiator,
			AmountRSD:             money.FormatAmount(amountRSD),
			UnitsDelta:            money.FormatAmount(unitsDelta),
			SourceOrDestAccountID: in.SourceAccountID,
			IsInflow:              true,
			Status:                domain.FundTxPending,
		})
		if err != nil {
			return err
		}
		auditID = row.ID
		txID = fundInvestTxID(row.ID)
		// Stamp the saga id on the row so the recovery worker can resume.
		_, err = s.Store.MarkFundTransactionStatus(ctx, tx, row.ID, domain.FundTxPending, "", txID, "")
		return err
	})
	if err != nil {
		return nil, err
	}

	payload := fundInvestPayload{
		TransactionRowID:    auditID,
		FundID:              f.ID,
		FundBankAccountID:   f.BankAccountID,
		ClientID:            investor,
		InitiatorEmployeeID: initiator,
		SourceAccountID:     in.SourceAccountID,
		SourceCurrency:      string(srcCurrency),
		SourceAmount:        money.FormatAmount(sourceAmount),
		AmountRSD:           money.FormatAmount(amountRSD),
		UnitsDelta:          money.FormatAmount(unitsDelta),
		IsActuary:           isInNameOfBank,
	}

	ctx = saga.FaultsFromMetadata(ctx, s.Cfg.SagaDebugFaultInjection)
	row, err := saga.Start(ctx, s.SagaOrch, saga.StartInput[fundInvestPayload]{
		TransactionID: txID,
		SagaType:      fundInvestSagaType,
		InitialState:  payload,
		AttemptsMax:   8,
	})
	if err != nil {
		s.log().ErrorContext(ctx, "fund invest saga failed",
			"err", err, "transaction_id", txID, "fund_id", f.ID)
		if mErr := s.markFundTxFailed(ctx, auditID, err.Error()); mErr != nil {
			s.log().WarnContext(ctx, "fund invest: mark tx failed errored",
				"err", mErr, "transaction_id", txID, "fund_tx_id", auditID)
		}
		return nil, fmt.Errorf("fund invest saga: %w", err)
	}
	if row.Status != saga.StatusCompleted {
		// Transient park: saga.Start suppresses the err, only signal
		// is row.Status=Running. Surface as Unavailable so the caller
		// polls/backoffs; recovery worker will drive it forward.
		// See [[reference_saga_park_status_mapping]] for the pattern.
		if row.Status == saga.StatusRunning {
			s.log().WarnContext(ctx, "fund invest saga parked for retry",
				"transaction_id", txID, "fund_id", f.ID, "last_error", row.LastError)
			return nil, status.Error(codes.Unavailable, "fund invest saga parked for retry")
		}
		s.log().ErrorContext(ctx, "fund invest saga did not complete",
			"transaction_id", txID, "fund_id", f.ID,
			"saga_status", string(row.Status), "last_error", row.LastError)
		return nil, apperr.Internal("fund invest saga did not complete", nil)
	}

	final, err := s.Store.GetFundTransaction(ctx, auditID)
	if err != nil {
		return nil, err
	}
	s.log().InfoContext(ctx, "fund invest completed",
		"transaction_id", txID, "fund_id", f.ID,
		"amount_rsd", payload.AmountRSD, "units_delta", payload.UnitsDelta)
	return &InvestInFundResult{Transaction: final, SagaID: txID, Pending: false}, nil
}

// convertFromRSDForFundInvest converts a committed RSD amount into the
// amount that must be debited from the investor's `srcCurrency` source
// account. The fund is always credited the full committed RSD (the
// caller passes amountRSD straight through as the commit DestAmount);
// when `applyCommission` is true (client path) the investor is charged
// the menjačnica fee on top so the fund still nets the whole figure —
// the exact inverse of the withdraw haircut in
// convertFromRSDForFundFlow (spec p.26 ASK on every leg; spec p.55
// actuaries pay no commission). Passes through unchanged when
// srcCurrency is already RSD: no FX leg, no commission, and
// bank.CommitReservedFunds then books a single same-currency leg
// with src == dst amount.
func (s *Service) convertFromRSDForFundInvest(
	ctx context.Context, srcCurrency domain.Currency, amountRSD *big.Rat,
	applyCommission bool,
) (*big.Rat, error) {
	if srcCurrency == domain.CurrencyRSD || srcCurrency == "" {
		return amountRSD, nil
	}
	if s.Rates == nil {
		return nil, apperr.FailedPrecondition("FX rate provider nije dostupan")
	}
	// RSD → src: divide by the src→RSD ASK (spec p.26 ASK on every leg;
	// the bank's profit is the commission, not the spread).
	_, ask, err := s.Rates.Quote(ctx, srcCurrency, domain.CurrencyRSD)
	if err != nil {
		return nil, apperr.Internal("fx quote failed", err)
	}
	r, err := money.Parse(ask)
	if err != nil || !money.IsPositive(r) {
		return nil, apperr.Internal("fx ask invalid", err)
	}
	out, err := money.Div(amountRSD, r)
	if err != nil {
		return nil, apperr.Internal("fx div failed", err)
	}
	if applyCommission {
		commission, err := money.Parse(s.Cfg.FXCommission)
		if err == nil && commission.Sign() > 0 {
			// Gross the debit up by 1/(1-c) so that after the bank's
			// (1-c) menjačnica haircut the fund still receives the
			// full committed RSD. Exact inverse of
			// convertFromRSDForFundFlow's out*(1-c).
			oneMinusC := money.Sub(money.MustParse("1"), commission)
			if !money.IsPositive(oneMinusC) {
				return nil, apperr.FailedPrecondition("FX provizija nije validna")
			}
			out, err = money.Div(out, oneMinusC)
			if err != nil {
				return nil, apperr.Internal("fx commission div failed", err)
			}
		}
	}
	if !money.IsPositive(out) {
		return nil, apperr.Validation("iznos nije validan posle konverzije")
	}
	return out, nil
}

// markFundTxFailed flips the audit row to `failed` with the given
// reason. Best-effort — failures here only log.
func (s *Service) markFundTxFailed(ctx context.Context, rowID, reason string) error {
	return s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		_, err := s.Store.MarkFundTransactionStatus(ctx, tx, rowID, domain.FundTxFailed, "", "", reason)
		return err
	})
}

// registerFundInvestSaga registers the invest definition.
func registerFundInvestSaga(reg *saga.Registry, svc *Service) {
	def := saga.Definition[fundInvestPayload]{
		Type: fundInvestSagaType,
		Steps: []saga.Step[fundInvestPayload]{
			// Step 1: reserve on the source account in source currency.
			{
				Name: "reserve_source",
				Forward: func(ctx context.Context, sc *saga.Context[fundInvestPayload]) error {
					_, err := svc.Reservations.Reserve(ctx, ReserveInput{
						AccountID: sc.State.SourceAccountID,
						Amount:    sc.State.SourceAmount,
						Currency:  domain.Currency(sc.State.SourceCurrency),
						OpID:      sc.OpID,
						OpKind:    "fund_invest",
					})
					return err
				},
				Compensate: func(ctx context.Context, sc *saga.Context[fundInvestPayload]) error {
					_, err := svc.Reservations.Release(ctx, sc.OpID)
					return err
				},
			},
			// Step 2: commit to the fund's RSD account.
			{
				Name: "transfer_to_fund",
				Forward: func(ctx context.Context, sc *saga.Context[fundInvestPayload]) error {
					reserveOp := saga.DeriveOpID(sc.TransactionID, "reserve_source")
					_, err := svc.Reservations.Commit(ctx, CommitInput{
						OpID:          reserveOp,
						DestAccountID: sc.State.FundBankAccountID,
						DestAmount:    sc.State.AmountRSD,
						DestCurrency:  domain.CurrencyRSD,
						IsActuary:     sc.State.IsActuary,
						Purpose:       "Uplata u investicioni fond — " + sc.State.FundID,
					})
					return err
				},
				// Best-effort compensation: re-release. After commit, the
				// bank's row is `committed` and release is a no-op; before
				// commit, release returns the funds.
				Compensate: func(ctx context.Context, sc *saga.Context[fundInvestPayload]) error {
					reserveOp := saga.DeriveOpID(sc.TransactionID, "reserve_source")
					_, err := svc.Reservations.Release(ctx, reserveOp)
					return err
				},
			},
			// Step 3: book the position + audit row + total_units.
			{
				Name: "record_position",
				Forward: func(ctx context.Context, sc *saga.Context[fundInvestPayload]) error {
					return svc.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
						if _, err := svc.Store.UpsertFundPositionInvest(ctx, tx,
							sc.State.FundID, sc.State.ClientID,
							sc.State.UnitsDelta, sc.State.AmountRSD,
						); err != nil {
							return err
						}
						if err := svc.Store.AdjustFundUnits(ctx, tx,
							sc.State.FundID, sc.State.UnitsDelta,
						); err != nil {
							return err
						}
						_, err := svc.Store.MarkFundTransactionStatus(ctx, tx,
							sc.State.TransactionRowID, domain.FundTxCompleted,
							sc.State.UnitsDelta, sc.TransactionID, "",
						)
						return err
					})
				},
				Compensate: func(ctx context.Context, sc *saga.Context[fundInvestPayload]) error {
					return svc.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
						// Reverse the position + units. CHECKs on the
						// columns surface a violation as
						// FailedPrecondition (permanent) which the
						// orchestrator marks the saga `failed` on.
						if _, err := svc.Store.DecrementFundPositionWithdraw(ctx, tx,
							sc.State.FundID, sc.State.ClientID,
							sc.State.UnitsDelta, sc.State.AmountRSD,
						); err != nil {
							return err
						}
						if err := svc.Store.AdjustFundUnits(ctx, tx,
							sc.State.FundID, "-"+sc.State.UnitsDelta,
						); err != nil {
							return err
						}
						_, err := svc.Store.MarkFundTransactionStatus(ctx, tx,
							sc.State.TransactionRowID, domain.FundTxFailed,
							"", "", "kompenzacija invest sage",
						)
						return err
					})
				},
			},
		},
	}
	saga.Register(reg, def)
}

// fundInvestTxID derives a deterministic transaction_id from the audit
// row's id. Re-running InvestInFund with the same audit row resumes.
func fundInvestTxID(rowID string) string {
	return uuid.NewSHA1(fundInvestNS, []byte(rowID)).String()
}

var fundInvestNS = uuid.MustParse("c4f0010d-be0b-4b4d-9b1d-d5a9c0e1b2f3")

// Permission helpers, kept private to the fund flows so the broader
// service doesn't depend on the bundle layout.
func (s *Service) requireFundsInvestClient(p auth.Principal) error {
	if permissions.HasAny(p.Permissions, permissions.Admin,
		permissions.TradingClient, permissions.FundsManageSupervisor) {
		return nil
	}
	return apperr.PermissionDenied("nedovoljne permisije za ulaganje u fond")
}

func (s *Service) requireFundsManage(p auth.Principal) error {
	if permissions.HasAny(p.Permissions, permissions.Admin, permissions.FundsManageSupervisor) {
		return nil
	}
	return apperr.PermissionDenied("nedovoljne permisije za upravljanje fondom")
}
