// Fund-withdraw SAGA (c4 PR3 — spec p.71-75).
//
// Two settlement paths share four logical steps. The liquid path runs
// when the fund's bank account currently has at least amount_rsd of
// available_balance; the illiquid path slots in a `liquidate_holdings`
// step that places fund-actor MARKET SELLs greedy by market value
// until enough RSD lands. The recovery worker re-enters the saga when
// the orders settle, and the saga then resumes at `transfer_to_target`.
//
// Forward steps
// =============
//
//   1. reserve_fund_balance   — ReserveFunds on the fund's bank account
//      for amount_rsd. On insufficient available_balance the step
//      flips the saga to `liquidate_holdings` (illiquid path); on
//      success the saga continues at `transfer_to_target` directly.
//      Compensation releases the reservation.
//
//   2. liquidate_holdings     — Illiquid path only. Cancel and replace
//      logic isn't strictly needed because c3 orders fire MARKET fills
//      directly; we just place orders and stamp their ids onto the
//      payload, then short-circuit the orchestrator until enough cash
//      lands. The saga's `next_attempt_at` is bumped by the recovery
//      tick so the orchestrator polls. Compensation cancels any still-
//      pending child orders.
//
//   3. transfer_to_target     — CommitReservedFunds from the fund's
//      account to the client's dest account in dest_currency (FX hop
//      via menjačnica if needed; commission ON for client withdrawer,
//      OFF for supervisor-on-behalf-of-bank).
//
//   4. update_position        — One tx: decrement units +
//      total_invested_rsd pro-rata on the client_fund_positions row;
//      decrement fund.total_units; insert a realized_gains row for the
//      client (EDGE-3 — taxation at the client boundary); flip the
//      audit row to `completed`.
//
// EDGE-3 taxation
// ===============
// Cost-basis removed (the part of total_invested_rsd attributed to
// the withdrawn units) is the position's total_invested_rsd × (units
// removed / position.units). Proceeds = amount_rsd. realized_gains.
// currency = RSD; gain_rsd = proceeds − cost_basis. The bank-as-client
// position (BankAsClientOwnerID) gets the same row so Profit Banke is
// consistent.

package service

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const fundWithdrawSagaType = "fund_withdraw"

// fundWithdrawPayload is the persisted state. AmountRSD is the figure
// reserved on the fund account; DestAmount is the post-commission
// amount credited to the dest account in DestCurrency.
type fundWithdrawPayload struct {
	TransactionRowID    string `json:"transaction_row_id"`
	FundID              string `json:"fund_id"`
	FundBankAccountID   string `json:"fund_bank_account_id"`
	ClientID            string `json:"client_id"`
	InitiatorEmployeeID string `json:"initiator_employee_id"`
	DestAccountID       string `json:"dest_account_id"`
	DestCurrency        string `json:"dest_currency"`
	DestAmount          string `json:"dest_amount"`
	AmountRSD           string `json:"amount_rsd"`     // gross RSD pulled from the fund
	UnitsRemoved        string `json:"units_removed"`
	CostBasisRemoved    string `json:"cost_basis_removed"`
	IsActuary           bool   `json:"is_actuary"`

	// Illiquid path: ids of the child orders the saga placed so the
	// recovery worker can poll their settlement status before resuming
	// `transfer_to_target`.
	ChildOrderIDs []string `json:"child_order_ids"`
	IlliquidPath  bool     `json:"illiquid_path"`
}

// WithdrawFromFundInput is the validated payload.
type WithdrawFromFundInput struct {
	FundID           string
	AmountRSD        string
	DestAccountID    string
	OnBehalfClientID string
	WithdrawAll      bool
}

// WithdrawFromFundResult is the FE response shape.
type WithdrawFromFundResult struct {
	Transaction *domain.FundTransaction
	SagaID      string
	Pending     bool
}

// WithdrawFromFund prepares the audit row + saga state and kicks off.
// When the liquid path completes synchronously the audit row reads
// `completed`; on the illiquid path it stays `pending` until the
// recovery worker resumes the saga after the child orders settle.
func (s *Service) WithdrawFromFund(ctx context.Context, in WithdrawFromFundInput) (*WithdrawFromFundResult, error) {
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
	if isInNameOfBank {
		if err := s.requireFundsManage(p); err != nil {
			return nil, err
		}
	} else {
		// Clients withdraw their own units; the funds.invest perm
		// covers both directions (no separate funds.withdraw perm).
		if err := s.requireFundsInvestClient(p); err != nil {
			return nil, err
		}
	}

	f, err := s.Store.GetFund(ctx, in.FundID)
	if err != nil {
		return nil, err
	}
	pos, err := s.Store.GetFundPosition(ctx, f.ID, investor)
	if err != nil {
		return nil, err
	}
	dec := s.decorateFund(ctx, f)

	// Determine the gross RSD figure being withdrawn + the units it
	// takes from the position.
	posUnits, err := money.Parse(pos.Units)
	if err != nil || !money.IsPositive(posUnits) {
		return nil, apperr.FailedPrecondition("pozicija nema dovoljno jedinica")
	}
	unitPrice, err := money.Parse(dec.UnitPriceRSD)
	if err != nil || !money.IsPositive(unitPrice) {
		unitPrice = money.MustParse("1")
	}
	positionValue := money.Mul(posUnits, unitPrice)

	var amountRSD *big.Rat
	if in.WithdrawAll {
		amountRSD = positionValue
	} else {
		amountRSD, err = money.Parse(in.AmountRSD)
		if err != nil || !money.IsPositive(amountRSD) {
			return nil, apperr.Validation("iznos nije validan")
		}
		if amountRSD.Cmp(positionValue) > 0 {
			return nil, apperr.FailedPrecondition("iznos prelazi trenutnu vrednost pozicije")
		}
	}

	unitsRemoved, err := money.Div(amountRSD, unitPrice)
	if err != nil {
		return nil, apperr.Internal("unit math failed", err)
	}
	if unitsRemoved.Cmp(posUnits) > 0 {
		// Rounding tolerance — clamp.
		unitsRemoved = posUnits
	}
	// Pro-rata cost basis removed.
	posInvested, _ := money.Parse(pos.TotalInvestedRSD)
	if posInvested == nil {
		posInvested = money.MustParse("0")
	}
	costBasisRemoved := money.MustParse("0")
	if posUnits.Sign() > 0 {
		frac, _ := money.Div(unitsRemoved, posUnits)
		costBasisRemoved = money.Mul(posInvested, frac)
	}

	// Dest currency lookup for FX policy.
	destCurrency, _, err := s.Reservations.AccountAvailable(ctx, in.DestAccountID)
	if err != nil {
		return nil, fmt.Errorf("bank.GetAccount(dest): %w", err)
	}

	// Convert RSD → dest currency (post-commission for clients).
	destAmount, err := s.convertFromRSDForFundFlow(ctx, destCurrency, amountRSD, !isInNameOfBank)
	if err != nil {
		return nil, err
	}

	initiator := ""
	if isInNameOfBank {
		initiator = p.UserID
	}

	var auditID, txID string
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		row, err := s.Store.InsertFundTransaction(ctx, tx, &domain.FundTransaction{
			FundID:                f.ID,
			ClientID:              investor,
			InitiatorEmployeeID:   initiator,
			AmountRSD:             money.FormatAmount(amountRSD),
			UnitsDelta:            "-" + money.FormatAmount(unitsRemoved),
			SourceOrDestAccountID: in.DestAccountID,
			IsInflow:              false,
			Status:                domain.FundTxPending,
		})
		if err != nil {
			return err
		}
		auditID = row.ID
		txID = fundWithdrawTxID(row.ID)
		_, err = s.Store.MarkFundTransactionStatus(ctx, tx, row.ID, domain.FundTxPending, "", txID, "")
		return err
	})
	if err != nil {
		return nil, err
	}

	payload := fundWithdrawPayload{
		TransactionRowID:    auditID,
		FundID:              f.ID,
		FundBankAccountID:   f.BankAccountID,
		ClientID:            investor,
		InitiatorEmployeeID: initiator,
		DestAccountID:       in.DestAccountID,
		DestCurrency:        string(destCurrency),
		DestAmount:          money.FormatAmount(destAmount),
		AmountRSD:           money.FormatAmount(amountRSD),
		UnitsRemoved:        money.FormatAmount(unitsRemoved),
		CostBasisRemoved:    money.FormatAmount(costBasisRemoved),
		IsActuary:           isInNameOfBank,
	}

	ctx = saga.FaultsFromMetadata(ctx, s.Cfg.SagaDebugFaultInjection)
	row, err := saga.Start(ctx, s.SagaOrch, saga.StartInput[fundWithdrawPayload]{
		TransactionID: txID,
		SagaType:      fundWithdrawSagaType,
		InitialState:  payload,
		AttemptsMax:   8,
	})
	// A transient error from a still-running saga is the illiquid path's
	// normal signal — `liquidate_holdings` returns codes.Unavailable to
	// park itself while the auto-liquidation orders settle, and the
	// recovery worker drives it forward. From the caller's perspective
	// this is "pending", not failure.
	if err != nil && row != nil && row.Status == saga.StatusRunning {
		err = nil
	}
	if err != nil {
		_ = s.markFundTxFailed(ctx, auditID, err.Error())
		return nil, fmt.Errorf("fund withdraw saga: %w", err)
	}
	final, _ := s.Store.GetFundTransaction(ctx, auditID)
	return &WithdrawFromFundResult{
		Transaction: final,
		SagaID:      txID,
		Pending:     row.Status == saga.StatusRunning,
	}, nil
}

// convertFromRSDForFundFlow inverts convertToRSDForFundFlow: amountRSD
// → amount in destCurrency, applying commission for client paths.
func (s *Service) convertFromRSDForFundFlow(
	ctx context.Context, destCurrency domain.Currency, amountRSD *big.Rat,
	applyCommission bool,
) (*big.Rat, error) {
	if destCurrency == domain.CurrencyRSD || destCurrency == "" {
		return amountRSD, nil
	}
	if s.Rates == nil {
		return nil, apperr.FailedPrecondition("FX rate provider nije dostupan")
	}
	// Going RSD → X: divide by X→RSD ask (matches spec p.26 ASK on
	// every leg; profit comes from the commission).
	_, ask, err := s.Rates.Quote(ctx, destCurrency, domain.CurrencyRSD)
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
			fee := money.Mul(out, commission)
			out = money.Sub(out, fee)
		}
	}
	if !money.IsPositive(out) {
		return nil, apperr.Validation("iznos premali posle provizije")
	}
	return out, nil
}

// registerFundWithdrawSaga registers the withdraw definition.
func registerFundWithdrawSaga(reg *saga.Registry, svc *Service) {
	def := saga.Definition[fundWithdrawPayload]{
		Type: fundWithdrawSagaType,
		Steps: []saga.Step[fundWithdrawPayload]{
			// Step 1: reserve. On insufficient available_balance fall
			// through to the illiquid path (placed orders).
			{
				Name: "reserve_fund_balance",
				Forward: func(ctx context.Context, sc *saga.Context[fundWithdrawPayload]) error {
					_, _, err := svc.Reservations.AccountAvailable(ctx, sc.State.FundBankAccountID)
					if err != nil {
						return err
					}
					_, err = svc.Reservations.Reserve(ctx, ReserveInput{
						AccountID: sc.State.FundBankAccountID,
						Amount:    sc.State.AmountRSD,
						Currency:  domain.CurrencyRSD,
						OpID:      sc.OpID,
						OpKind:    "fund_withdraw",
					})
					if err == nil {
						return nil
					}
					// FailedPrecondition from bank means available_balance
					// underflow — flip to illiquid path. We swallow the
					// error and let the orchestrator advance; subsequent
					// steps handle the illiquid case.
					if isAvailableBalanceUnderflow(err) {
						sc.State.IlliquidPath = true
						sc.Log.Info("fund withdraw: insufficient liquid, switching to auto-liquidation")
						return nil
					}
					return err
				},
				Compensate: func(ctx context.Context, sc *saga.Context[fundWithdrawPayload]) error {
					_, err := svc.Reservations.Release(ctx, sc.OpID)
					return err
				},
			},
			// Step 2: place auto-liquidation orders. Skip on the liquid
			// path (IlliquidPath=false from step 1). Returns nil only
			// when enough proceeds have already landed on the fund
			// account; otherwise returns a transient error so the
			// recovery worker retries.
			{
				Name: "liquidate_holdings",
				Forward: func(ctx context.Context, sc *saga.Context[fundWithdrawPayload]) error {
					if !sc.State.IlliquidPath {
						return nil
					}
					return svc.liquidateForWithdraw(ctx, sc)
				},
				Compensate: func(ctx context.Context, sc *saga.Context[fundWithdrawPayload]) error {
					// Cancel any still-active child orders.
					for _, id := range sc.State.ChildOrderIDs {
						if _, err := svc.cancelOrderInternal(ctx, id); err != nil {
							sc.Log.Warn("compensate liquidate: cancel failed", "order_id", id, "err", err.Error())
						}
					}
					return nil
				},
			},
			// Step 3: commit to the destination account.
			{
				Name: "transfer_to_target",
				Forward: func(ctx context.Context, sc *saga.Context[fundWithdrawPayload]) error {
					// On the illiquid path we may need to reserve here
					// because step 1 short-circuited.
					reserveOp := saga.DeriveOpID(sc.TransactionID, "reserve_fund_balance")
					if sc.State.IlliquidPath {
						_, err := svc.Reservations.Reserve(ctx, ReserveInput{
							AccountID: sc.State.FundBankAccountID,
							Amount:    sc.State.AmountRSD,
							Currency:  domain.CurrencyRSD,
							OpID:      reserveOp,
							OpKind:    "fund_withdraw",
						})
						if err != nil {
							return err
						}
					}
					_, err := svc.Reservations.Commit(ctx, CommitInput{
						OpID:          reserveOp,
						DestAccountID: sc.State.DestAccountID,
						DestAmount:    sc.State.DestAmount,
						DestCurrency:  domain.Currency(sc.State.DestCurrency),
						IsActuary:     sc.State.IsActuary,
						Purpose:       "Povlačenje iz investicionog fonda — " + sc.State.FundID,
					})
					return err
				},
				Compensate: func(ctx context.Context, sc *saga.Context[fundWithdrawPayload]) error {
					reserveOp := saga.DeriveOpID(sc.TransactionID, "reserve_fund_balance")
					_, err := svc.Reservations.Release(ctx, reserveOp)
					return err
				},
			},
			// Step 4: update position + realized_gain + audit row.
			{
				Name: "update_position",
				Forward: func(ctx context.Context, sc *saga.Context[fundWithdrawPayload]) error {
					return svc.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
						if _, err := svc.Store.DecrementFundPositionWithdraw(ctx, tx,
							sc.State.FundID, sc.State.ClientID,
							sc.State.UnitsRemoved, sc.State.CostBasisRemoved,
						); err != nil {
							return err
						}
						if err := svc.Store.AdjustFundUnits(ctx, tx,
							sc.State.FundID, "-"+sc.State.UnitsRemoved,
						); err != nil {
							return err
						}
						// EDGE-3 — realized_gain at the client boundary.
						gain := money.Sub(
							money.MustParse(sc.State.AmountRSD),
							money.MustParse(sc.State.CostBasisRemoved),
						)
						userKind := domain.KindClient
						if sc.State.ClientID == BankAsClientOwnerID {
							// Bank stake — record as employee kind so tax
							// aggregator picks it up alongside actuary gains.
							userKind = domain.KindEmployee
						}
						_, err := svc.Store.InsertRealizedGain(ctx, tx, &domain.RealizedGain{
							UserID:       sc.State.ClientID,
							UserKind:     userKind,
							FundID:       sc.State.FundID,
							AccountID:    sc.State.DestAccountID,
							Quantity:     1,
							CostBasisAmt: sc.State.CostBasisRemoved,
							ProceedsAmt:  sc.State.AmountRSD,
							Currency:     domain.CurrencyRSD,
							GainNative:   money.FormatAmount(gain),
							GainRSD:      money.FormatAmount(gain),
						})
						if err != nil {
							return err
						}
						_, err = svc.Store.MarkFundTransactionStatus(ctx, tx,
							sc.State.TransactionRowID, domain.FundTxCompleted,
							"", sc.TransactionID, "",
						)
						return err
					})
				},
				Compensate: nil, // terminal; if this step fails we leave the saga `failed` and the cash is on the dest account (manual reconcile).
			},
		},
	}
	saga.Register(reg, def)
}

// liquidateForWithdraw places fund-actor MARKET sells greedy by
// market value until projected proceeds ≥ shortfall. Returns nil only
// when the fund's account already has at least amount_rsd of
// available_balance (the recovery worker resumes after orders settle).
// EDGE-6: greedy-largest-first; tunable policy.
func (s *Service) liquidateForWithdraw(ctx context.Context, sc *saga.Context[fundWithdrawPayload]) error {
	// Re-quote the fund's available balance. If we're already liquid,
	// short-circuit to the next step.
	_, availStr, err := s.Reservations.AccountAvailable(ctx, sc.State.FundBankAccountID)
	if err != nil {
		return err
	}
	avail, _ := money.Parse(availStr)
	if avail == nil {
		avail = money.MustParse("0")
	}
	want, _ := money.Parse(sc.State.AmountRSD)
	if want != nil && avail.Cmp(want) >= 0 {
		// Enough liquidity — no more orders needed; let the saga advance.
		return nil
	}
	// Place new sell orders if we haven't already exhausted the basket.
	if len(sc.State.ChildOrderIDs) == 0 {
		if err := s.placeAutoLiquidationOrders(ctx, sc); err != nil {
			return err
		}
		// Returning transient so the orchestrator schedules a recovery
		// tick — the recovery worker will re-enter when the orders
		// have had time to settle.
		return status.Error(codes.Unavailable, "auto-liquidation orders placed; awaiting settlement")
	}
	// Already placed — check whether they're all done.
	allDone, err := s.allOrdersDone(ctx, sc.State.ChildOrderIDs)
	if err != nil {
		return err
	}
	if !allDone {
		return status.Error(codes.Unavailable, "auto-liquidation orders still pending")
	}
	return nil
}

// placeAutoLiquidationOrders walks the fund's holdings sorted by
// market value descending and creates MARKET sell orders until the
// projected proceeds clear the shortfall.
func (s *Service) placeAutoLiquidationOrders(ctx context.Context, sc *saga.Context[fundWithdrawPayload]) error {
	holdings, err := s.Store.ListHoldings(ctx, store.HoldingFilter{
		UserID: sc.State.FundID, UserKind: domain.KindFund,
	})
	if err != nil {
		return err
	}
	if len(holdings) == 0 {
		return apperr.FailedPrecondition("fond nema raspoloživih hartija za likvidaciju")
	}
	type rankedHolding struct {
		h         *domain.Holding
		sec       *domain.Security
		listing   *domain.Listing
		marketVal *big.Rat
	}
	rows := make([]rankedHolding, 0, len(holdings))
	for _, h := range holdings {
		sec, err := s.Store.GetSecurity(ctx, h.SecurityID)
		if err != nil {
			continue
		}
		if sec.Type == domain.SecurityForex || sec.Type == domain.SecurityOption {
			// Funds settle through MARKET sell orders against listings;
			// forex/option flows aren't applicable here.
			continue
		}
		listing, err := s.Store.GetListingBySecurityID(ctx, sec.ID)
		if err != nil {
			continue
		}
		price, _ := money.Parse(listing.Price)
		cs, _ := money.Parse(listing.ContractSize)
		if cs == nil || cs.Sign() == 0 {
			cs = money.MustParse("1")
		}
		qty := new(big.Rat).SetInt64(int64(h.Quantity))
		mkt := money.Mul(money.Mul(qty, cs), price)
		// FX into RSD for comparison with shortfall.
		if sec.Currency != domain.CurrencyRSD && sec.Currency != "" && s.Rates != nil {
			_, ask, err := s.Rates.Quote(ctx, sec.Currency, domain.CurrencyRSD)
			if err == nil {
				if r, perr := money.Parse(ask); perr == nil {
					mkt = money.Mul(mkt, r)
				}
			}
		}
		rows = append(rows, rankedHolding{h: h, sec: sec, listing: listing, marketVal: mkt})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].marketVal.Cmp(rows[j].marketVal) > 0
	})

	// Pull current liquid balance to know how much we need to raise.
	_, availStr, err := s.Reservations.AccountAvailable(ctx, sc.State.FundBankAccountID)
	if err != nil {
		return err
	}
	avail, _ := money.Parse(availStr)
	if avail == nil {
		avail = money.MustParse("0")
	}
	want, _ := money.Parse(sc.State.AmountRSD)
	shortfall := money.Sub(want, avail)
	if !money.IsPositive(shortfall) {
		return nil
	}

	var ids []string
	for _, r := range rows {
		if !money.IsPositive(shortfall) {
			break
		}
		// Sell whole holding for simplicity; service can refine to
		// partial-qty later. Cancellation on compensation is whole-row
		// anyway.
		order, err := s.createFundActorOrder(ctx, fundActorOrderInput{
			FundID:        sc.State.FundID,
			SecurityID:    r.sec.ID,
			AccountID:     sc.State.FundBankAccountID,
			Quantity:      r.h.Quantity,
			Direction:     domain.DirectionSell,
			OrderType:     domain.OrderMarket,
			AllOrNone:     true,
			InitiatorUser: sc.State.InitiatorEmployeeID,
		})
		if err != nil {
			sc.Log.Warn("auto-liquidation order failed", "ticker", r.sec.Ticker, "err", err.Error())
			continue
		}
		ids = append(ids, order.ID)
		shortfall = money.Sub(shortfall, r.marketVal)
	}
	if len(ids) == 0 {
		return apperr.FailedPrecondition("nije uspelo postavljanje naloga za auto-likvidaciju")
	}
	sc.State.ChildOrderIDs = ids
	return nil
}

// allOrdersDone returns true when every id in `ids` is either done or
// cancelled. Errors propagate.
func (s *Service) allOrdersDone(ctx context.Context, ids []string) (bool, error) {
	for _, id := range ids {
		o, err := s.Store.GetOrder(ctx, id)
		if err != nil {
			return false, err
		}
		if !o.IsDone && !o.Cancelled {
			return false, nil
		}
	}
	return true, nil
}

// cancelOrderInternal cancels an order without checking the caller's
// permissions (used by saga compensations). Returns the cancelled row.
func (s *Service) cancelOrderInternal(ctx context.Context, id string) (*domain.Order, error) {
	o, err := s.Store.GetOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	if o.IsDone || o.Cancelled {
		return o, nil
	}
	return s.Store.CancelOrder(ctx, id)
}

// fundWithdrawTxID derives the deterministic saga transaction id.
func fundWithdrawTxID(rowID string) string {
	return uuid.NewSHA1(fundWithdrawNS, []byte(rowID)).String()
}

var fundWithdrawNS = uuid.MustParse("c4f0011d-3f1b-4b4d-9b1d-d5a9c0e1b2f3")

// isAvailableBalanceUnderflow returns true when a bank.ReserveFunds
// call failed because the reservation amount exceeded available_balance.
// The bank side surfaces this as Validation (apperr); the resulting
// gRPC code is InvalidArgument with a Serbian message containing
// "raspolož" (the word's root in raspoloživo). Distinct from generic
// FailedPrecondition so the saga doesn't fall back to the illiquid
// path for the wrong-account-kind case.
func isAvailableBalanceUnderflow(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	if st.Code() != codes.InvalidArgument && st.Code() != codes.FailedPrecondition {
		return false
	}
	msg := strings.ToLower(st.Message())
	return strings.Contains(msg, "raspolož") ||
		strings.Contains(msg, "available_balance") ||
		strings.Contains(msg, "nedovoljno")
}
