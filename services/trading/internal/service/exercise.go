// Option exercise (spec p.61.d). An actuary holding an in-the-money
// option may exercise it before settlementDate:
//
//   * CALL — buy `qty × contract_size` of the underlying at the strike
//     price; cash leg debits the actuary's account, weighted-avg cost
//     basis on the new shares is the strike.
//   * PUT  — sell `qty × contract_size` of the underlying at the strike
//     price; cash leg credits the actuary's account, realized gain is
//     `(strike − cost_basis) × qty × contract_size` per spec p.62.
//
// Two-phase saga (mirrors executeFill): pre-write a pending row
// in option_exercises, call bank.SettleTrade with that row's UUID as
// op_id (idempotent at bank layer), then mark settled + apply portfolio
// changes in one trading-side tx.

package service

import (
	"context"
	"math/big"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ExerciseOptionInput captures the validated request payload.
type ExerciseOptionInput struct {
	HoldingID string
	Quantity  int32
}

// ExerciseOptionResult is the post-state surfaced to the caller. The
// underlying holding may have quantity=0 after a PUT exercise that
// closed the position; the row is left in place for audit.
type ExerciseOptionResult struct {
	OptionHolding     *domain.Holding
	UnderlyingHolding *domain.Holding
	BankOpID          string
	// PUT only — both zero on a CALL exercise.
	RealizedGainNative   string
	RealizedGainRSD      string
	RealizedGainCurrency domain.Currency
}

// ExerciseOption runs the spec p.61.d exercise saga. Permission gate:
// the caller must be an actuary (per `permissions.Actuary`); spec
// reserves the action for "Aktuari jer jedino oni mogu da kupuju
// opcije sa berze".
func (s *Service) ExerciseOption(ctx context.Context, in ExerciseOptionInput) (*ExerciseOptionResult, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.IsActuary(p.Permissions) {
		return nil, apperr.PermissionDenied("samo aktuari mogu da iskoriste opciju")
	}
	if in.Quantity <= 0 {
		return nil, apperr.Validation("quantity mora biti pozitivan")
	}

	holding, err := s.Store.GetHoldingByID(ctx, in.HoldingID)
	if err != nil {
		return nil, err
	}
	if holding.UserID != p.UserID || holding.UserKind != domain.UserKind(p.UserKind) {
		return nil, apperr.PermissionDenied("opcija nije u vašem portfoliu")
	}
	if holding.Quantity < in.Quantity {
		return nil, apperr.FailedPrecondition("nedovoljno opcija za iskorišćavanje")
	}

	option, err := s.Store.GetSecurity(ctx, holding.SecurityID)
	if err != nil {
		return nil, err
	}
	if option.Type != domain.SecurityOption {
		return nil, apperr.Validation("hartija nije opcija")
	}
	if option.SettlementDate == nil || !option.SettlementDate.After(s.now()) {
		return nil, apperr.FailedPrecondition("opcija je istekla")
	}
	if option.UnderlyingSecurityID == "" {
		s.log().ErrorContext(ctx, "option exercise: option has no underlying",
			"holding_id", holding.ID, "security_id", option.ID, "ticker", option.Ticker)
		return nil, apperr.Internal("opcija bez underlying-a", nil)
	}

	underlying, err := s.Store.GetSecurity(ctx, option.UnderlyingSecurityID)
	if err != nil {
		return nil, err
	}
	listing, err := s.Store.GetListingBySecurityID(ctx, underlying.ID)
	if err != nil {
		return nil, apperr.FailedPrecondition("nedostaje cena underlying hartije")
	}

	// ITM check per spec p.61.d. CALL profits when underlying > strike;
	// PUT profits when underlying < strike. Equality is OOM (no edge in
	// exercising).
	current, err := money.Parse(listing.Price)
	if err != nil {
		s.log().ErrorContext(ctx, "option exercise: underlying price unparseable",
			"err", err, "ticker", option.Ticker, "price", listing.Price)
		return nil, apperr.Internal("underlying price unparseable", err)
	}
	strike, err := money.Parse(option.StrikePrice)
	if err != nil {
		s.log().ErrorContext(ctx, "option exercise: strike unparseable",
			"err", err, "ticker", option.Ticker, "strike_price", option.StrikePrice)
		return nil, apperr.Internal("strike unparseable", err)
	}
	switch option.OptionType {
	case domain.OptionCall:
		if current.Cmp(strike) <= 0 {
			return nil, apperr.FailedPrecondition("opcija nije in-the-money (CALL)")
		}
	case domain.OptionPut:
		if current.Cmp(strike) >= 0 {
			return nil, apperr.FailedPrecondition("opcija nije in-the-money (PUT)")
		}
	default:
		return nil, apperr.Validation("nepoznat tip opcije")
	}

	// Notional in the underlying's currency: qty × contract_size × strike.
	contractSizeStr := option.ContractSize
	if contractSizeStr == "" {
		contractSizeStr = "1"
	}
	cs, err := money.Parse(contractSizeStr)
	if err != nil {
		s.log().ErrorContext(ctx, "option exercise: contract size unparseable",
			"err", err, "ticker", option.Ticker, "contract_size", contractSizeStr)
		return nil, apperr.Internal("contract size unparseable", err)
	}
	if cs.Sign() == 0 {
		cs = money.MustParse("1")
	}
	qtyR := new(big.Rat).SetInt64(int64(in.Quantity))
	notional := money.Mul(money.Mul(qtyR, cs), strike)
	if !money.IsPositive(notional) {
		return nil, apperr.Validation("notional iznos nije pozitivan")
	}
	currency := underlying.Currency
	if currency == "" {
		currency = domain.CurrencyRSD
	}

	// (1) Pre-write pending exercise row in its own tx so its UUID
	// survives a crash in the bank call.
	pending := &domain.OptionExercise{
		OptionHoldingID:      holding.ID,
		UserID:               holding.UserID,
		UserKind:             holding.UserKind,
		OptionSecurityID:     option.ID,
		UnderlyingSecurityID: underlying.ID,
		AccountID:            holding.AccountID,
		OptionType:           option.OptionType,
		Quantity:             in.Quantity,
		ContractSize:         contractSizeStr,
		StrikePrice:          option.StrikePrice,
		NotionalAmt:          money.FormatAmount(notional),
		Currency:             currency,
	}
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		row, err := s.Store.InsertPendingOptionExercise(ctx, tx, pending)
		if err != nil {
			return err
		}
		pending = row
		return nil
	}); err != nil {
		s.log().ErrorContext(ctx, "option exercise: pending row insert failed",
			"err", err, "holding_id", holding.ID, "ticker", option.Ticker)
		return nil, err
	}

	// (2) Bank cash leg. CALL = debit user, PUT = credit user. The bank
	// dedupes on op_id via migration 0011's unique (op_id, leg_index).
	if s.Settler == nil {
		s.log().ErrorContext(ctx, "option exercise: trade settler not wired",
			"exercise_id", pending.ID)
		return nil, apperr.Internal("trade settler not wired", nil)
	}
	direction := "debit"
	if option.OptionType == domain.OptionPut {
		direction = "credit"
	}
	settledOpID, err := s.Settler.Settle(ctx, SettleInput{
		AccountID: holding.AccountID,
		Direction: direction,
		Currency:  currency,
		Amount:    money.FormatAmount(notional),
		OpID:      pending.ID,
		IsActuary: p.UserKind == auth.KindEmployee,
		Purpose:   "Exercise " + option.Ticker,
	})
	if err != nil {
		s.log().ErrorContext(ctx, "option exercise: bank settle failed",
			"err", err, "exercise_id", pending.ID, "ticker", option.Ticker,
			"direction", direction, "amount", pending.NotionalAmt,
			"currency", string(currency))
		return nil, err
	}

	// (3) Mark settled + apply portfolio changes in one tx. Cancellation
	// has no equivalent here (one-shot user action), but we still want
	// the booking to be all-or-nothing against the bank settle.
	res := &ExerciseOptionResult{BankOpID: settledOpID, RealizedGainCurrency: currency}
	underlyingDelta := int32(0)
	{
		// The contract-size bucket the actuary moves on the underlying.
		// E.g. exercising 2 contracts × 100 share size = 200 shares.
		csR, _ := money.Parse(contractSizeStr)
		shares := money.Mul(qtyR, csR)
		if !shares.IsInt() {
			return nil, apperr.Validation("količina × contract_size nije ceo broj")
		}
		v := shares.Num().Int64()
		if int64(int32(v)) != v {
			return nil, apperr.Validation("količina × contract_size prelazi int32")
		}
		underlyingDelta = int32(v)
	}

	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		var realizedGainID string

		// Apply the underlying-side change first so its ID is available
		// when stamping the exercise row.
		switch option.OptionType {
		case domain.OptionCall:
			h, err := s.Store.ApplyBuyFill(ctx, tx,
				holding.UserID, string(holding.UserKind),
				underlying.ID, holding.AccountID,
				underlyingDelta, option.StrikePrice,
			)
			if err != nil {
				return err
			}
			res.UnderlyingHolding = h
		case domain.OptionPut:
			avgPrice, h, err := s.Store.ApplySellFill(ctx, tx,
				holding.UserID, string(holding.UserKind),
				underlying.ID, holding.AccountID,
				underlyingDelta,
			)
			if err != nil {
				return err
			}
			res.UnderlyingHolding = h
			rg, err := s.recordExerciseRealizedGain(ctx, tx, &exerciseRealizedGainInput{
				UserID:       holding.UserID,
				UserKind:     holding.UserKind,
				SecurityID:   underlying.ID,
				AccountID:    holding.AccountID,
				Quantity:     underlyingDelta,
				SellPrice:    option.StrikePrice,
				CostBasis:    avgPrice,
				ContractSize: "1",
				Currency:     currency,
			})
			if err != nil {
				return err
			}
			realizedGainID = rg.ID
			res.RealizedGainNative = rg.GainNative
			res.RealizedGainRSD = rg.GainRSD
		}

		// Consume the option contracts.
		opt, err := s.Store.AdjustHoldingQuantity(ctx, tx, holding.ID, -in.Quantity)
		if err != nil {
			return err
		}
		res.OptionHolding = opt

		if err := s.Store.MarkOptionExerciseSettled(ctx, tx, pending.ID, settledOpID, realizedGainID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		s.Log.Error("option exercise booking failed after bank settle",
			"exercise_id", pending.ID, "op_id", settledOpID, "err", err.Error())
		return nil, err
	}
	s.log().InfoContext(ctx, "option exercised",
		"exercise_id", pending.ID, "op_id", settledOpID, "ticker", option.Ticker,
		"option_type", string(option.OptionType), "quantity", in.Quantity,
		"notional", pending.NotionalAmt, "currency", string(currency))
	return res, nil
}

// exerciseRealizedGainInput is a thin sibling of recordRealizedGain's
// implicit (Order, …) inputs — exercise has no Order to point at.
type exerciseRealizedGainInput struct {
	UserID       string
	UserKind     domain.UserKind
	SecurityID   string
	AccountID    string
	Quantity     int32
	SellPrice    string
	CostBasis    string
	ContractSize string
	Currency     domain.Currency
}

func (s *Service) recordExerciseRealizedGain(
	ctx context.Context, tx pgx.Tx, in *exerciseRealizedGainInput,
) (*domain.RealizedGain, error) {
	sell, err := money.Parse(in.SellPrice)
	if err != nil {
		return nil, apperr.Internal("sell price unparseable", err)
	}
	cost, err := money.Parse(in.CostBasis)
	if err != nil {
		return nil, apperr.Internal("cost basis unparseable", err)
	}
	cs, err := money.Parse(in.ContractSize)
	if err != nil || cs.Sign() == 0 {
		cs = money.MustParse("1")
	}
	q := new(big.Rat).SetInt64(int64(in.Quantity))
	gainNative := money.Mul(money.Mul(q, cs), money.Sub(sell, cost))

	cur := in.Currency
	if cur == "" {
		cur = domain.CurrencyRSD
	}
	var gainRSD *big.Rat
	if cur == domain.CurrencyRSD || s.Rates == nil {
		gainRSD = new(big.Rat).Set(gainNative)
	} else {
		_, ask, err := s.Rates.Quote(ctx, cur, domain.CurrencyRSD)
		if err == nil {
			r, perr := money.Parse(ask)
			if perr == nil {
				gainRSD = money.Mul(gainNative, r)
			}
		}
		if gainRSD == nil {
			s.Log.Warn("rsd conversion for exercise gain failed; using native value",
				"currency", cur)
			gainRSD = new(big.Rat).Set(gainNative)
		}
	}

	costBasisAmt := money.Mul(money.Mul(q, cs), cost)
	proceedsAmt := money.Mul(money.Mul(q, cs), sell)

	return s.Store.InsertRealizedGain(ctx, tx, &domain.RealizedGain{
		UserID:       in.UserID,
		UserKind:     in.UserKind,
		SecurityID:   in.SecurityID,
		AccountID:    in.AccountID,
		Quantity:     in.Quantity,
		CostBasisAmt: money.FormatAmount(costBasisAmt),
		ProceedsAmt:  money.FormatAmount(proceedsAmt),
		Currency:     cur,
		GainNative:   money.FormatAmount(gainNative),
		GainRSD:      money.FormatAmount(gainRSD),
	})
}

// keep time import live for s.now() callers in this file even when
// build tags strip the rest.
var _ = time.Now
