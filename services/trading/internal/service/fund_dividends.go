// Fund dividend distribution (todoSpec C4 S69-S72).
//
// A fund that holds dividend-paying stock is just another dividend
// candidate in the quarterly cron's ListDividendCandidates walk
// (user_kind='fund'). When RunDividendPayout hits such a holding it
// routes here instead of the individual-holder path. The computation
// and credit mechanism are IDENTICAL to the individual holder (S72):
// the same pkg/dividend math produced `gross`, the same
// bank.SettleDividend credits an account, the same dividend_payouts row
// records the receipt. Only the routing + two fund-specific extras
// differ:
//
//   S69 — the dividend is credited to the FUND's own RSD bank account,
//         so the fund's liquid cash (LikvidnaSredstva / liquid_rsd)
//         grows by the received amount. bank.SettleDividend converts the
//         security-currency dividend to RSD commission-free on credit.
//
//   S71 — the received dividend is attributed across the fund's client
//         investors proportional to their unit share at payout time
//         (client A 30% / B 70% of 10.000 RSD → 3.000 / 7.000). The cash
//         lands on the fund's account; each investor's economic slice is
//         reflected through the unit price (their position appreciates).
//         fund_dividend_distributions records the per-client RSD slice so
//         the attribution is auditable.
//
//   S70 — when reinvest_dividends is enabled the cron immediately places
//         a MARKET BUY for the received dividend amount through the
//         fund's account (compounding), reusing the existing fund-actor
//         order path.

package service

import (
	"context"
	"math/big"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/jackc/pgx/v5"
)

// payFundDividend credits a fund-owned holding's dividend to the fund's
// RSD bank account (S69), records the payout (S72), distributes it across
// the fund's investors proportionally (S71), and reinvests it when the
// fund opts in (S70). Returns the payout converted to RSD (for the run
// total), mirroring payOneDividend's contract.
func (s *Service) payFundDividend(ctx context.Context, c *store.DividendCandidate, gross *big.Rat, year, quarter int) (*big.Rat, error) {
	// The fund the holding belongs to (UserID == fund.id for fund-owned
	// holdings, set by createFundActorOrder).
	f, err := s.Store.GetFund(ctx, c.UserID)
	if err != nil {
		return nil, err
	}

	// S69 — credit the dividend onto the fund's own RSD account. The
	// fund account's currency is RSD, so bank.SettleDividend converts the
	// security-currency dividend commission-free (same path S56 uses for
	// an RSD-only client holder). liquid_rsd grows by the converted
	// amount.
	opID := dividendOpID(c.HoldingID, year, quarter)
	grossStr := money.FormatAmount(gross)
	settledOpID, err := s.Reservations.SettleDividend(ctx, DividendSettleInput{
		AccountID: f.BankAccountID,
		Amount:    grossStr,
		Currency:  c.Currency,
		OpID:      opID,
		Purpose:   "Isplata dividende fondu",
	})
	if err != nil {
		return nil, err
	}
	if settledOpID != "" {
		opID = settledOpID
	}

	// The dividend in RSD — the amount actually credited to the fund's
	// RSD account and the basis for the proportional split (S71) and the
	// reinvest BUY (S70).
	rsd := s.dividendRSD(ctx, gross, c.Currency)

	// S72 — record the receipt exactly like the individual-holder path
	// (user_kind='fund'). Funds aren't taxable entities at receipt
	// (EDGE-3: tax bites the client at withdrawal), so tax_rsd stays 0.
	paidAt := s.now()
	payout, err := s.Store.InsertDividendPayout(ctx, &domain.DividendPayout{
		UserID:      c.UserID,
		UserKind:    domain.KindFund,
		SecurityID:  c.SecurityID,
		Quantity:    c.Quantity,
		Price:       c.Price,
		GrossAmount: grossStr,
		Currency:    c.Currency,
		AccountID:   f.BankAccountID,
		TaxRSD:      "0",
		OpID:        opID,
		Status:      "paid",
		PaidAt:      &paidAt,
	})
	if err != nil {
		return nil, err
	}

	// S71 — attribute the RSD dividend across the fund's investors
	// proportional to their unit share at payout time.
	if err := s.distributeFundDividend(ctx, f, payout, rsd); err != nil {
		// Distribution is an accounting ledger on top of the (already
		// credited + recorded) dividend; a failure here must not abort
		// the run or unwind the credit. Log and carry on.
		s.Log.Warn("fund dividend distribution failed",
			"fund_id", f.ID, "payout_id", payout.ID, "err", err.Error())
	}

	// S70 — reinvest: place a MARKET BUY for the received dividend amount.
	if f.ReinvestDividends {
		if err := s.reinvestFundDividend(ctx, f, c); err != nil {
			s.Log.Warn("fund dividend reinvest failed",
				"fund_id", f.ID, "security_id", c.SecurityID, "err", err.Error())
		}
	}

	s.log().InfoContext(ctx, "fund dividend paid",
		"fund_id", f.ID, "payout_id", payout.ID,
		"gross", grossStr, "currency", string(c.Currency))
	return rsd, nil
}

// distributeFundDividend records each investor's proportional slice of a
// fund dividend (S71). slice_i = dividend_rsd × units_i / total_units.
// Idempotent: the per-(payout, client) unique index makes a retried cron
// run converge. A fund with no units yet (or no positions) records
// nothing — the cash simply sits as liquid_rsd until someone invests.
func (s *Service) distributeFundDividend(ctx context.Context, f *domain.Fund, payout *domain.DividendPayout, rsd *big.Rat) error {
	totalUnits, err := money.Parse(f.TotalUnits)
	if err != nil || totalUnits.Sign() <= 0 {
		if err != nil {
			s.log().WarnContext(ctx, "fund total_units unparseable; skipping dividend distribution",
				"err", err, "fund_id", f.ID, "total_units", f.TotalUnits)
		}
		return nil
	}
	positions, err := s.Store.ListFundPositions(ctx, store.FundPositionFilter{
		FundID: f.ID, Status: "active",
	})
	if err != nil {
		return err
	}
	if len(positions) == 0 {
		return nil
	}
	totalUnitsStr := money.FormatAmount(totalUnits)
	return s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		for _, pos := range positions {
			units, err := money.Parse(pos.Units)
			if err != nil || units.Sign() <= 0 {
				if err != nil {
					s.log().WarnContext(ctx, "fund position units unparseable; skipping slice",
						"err", err, "fund_id", f.ID, "client_id", pos.ClientID, "units", pos.Units)
				}
				continue
			}
			slice := fundDividendSlice(rsd, units, totalUnits)
			if slice == nil {
				continue
			}
			if _, err := s.Store.InsertFundDividendDistribution(ctx, tx, &domain.FundDividendDistribution{
				FundID:           f.ID,
				DividendPayoutID: payout.ID,
				ClientID:         pos.ClientID,
				ShareUnits:       pos.Units,
				FundTotalUnits:   totalUnitsStr,
				AmountRSD:        money.FormatAmount(slice),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// fundDividendSlice returns one investor's proportional slice of an RSD
// dividend: rsd × units / totalUnits (S71). Returns nil when the inputs
// are non-positive (no slice). Pure — unit-tested directly.
func fundDividendSlice(rsd, units, totalUnits *big.Rat) *big.Rat {
	if rsd == nil || units == nil || totalUnits == nil ||
		units.Sign() <= 0 || totalUnits.Sign() <= 0 {
		return nil
	}
	frac, err := money.Div(units, totalUnits)
	if err != nil {
		return nil
	}
	return money.Mul(rsd, frac)
}

// reinvestQuantity returns the whole-share count a security-currency
// dividend `gross` buys at `price` (S70): floor(gross / price), clamped
// to a positive int32. Zero when the dividend can't afford a whole share.
// Pure — unit-tested directly.
func reinvestQuantity(gross, price *big.Rat) int32 {
	if gross == nil || price == nil || price.Sign() <= 0 || gross.Sign() <= 0 {
		return 0
	}
	qtyRat, err := money.Div(gross, price)
	if err != nil {
		return 0
	}
	qty := new(big.Int).Quo(qtyRat.Num(), qtyRat.Denom()).Int64()
	if qty <= 0 {
		return 0
	}
	if qty > 1<<31-1 {
		qty = 1<<31 - 1
	}
	return int32(qty)
}

// reinvestFundDividend places a MARKET BUY for the received dividend
// (S70). It compounds back into the dividend-paying security: quantity =
// floor(gross / price) in the security's own currency (the listing price
// is in that currency). A dividend too small to buy a whole share is a
// no-op (the cash stays liquid). Reuses the fund-actor order path so the
// buy settles through the fund's bank account, auto-approved, with no FX
// commission (spec p.55).
func (s *Service) reinvestFundDividend(ctx context.Context, f *domain.Fund, c *store.DividendCandidate) error {
	price, err := money.Parse(c.Price)
	if err != nil || price.Sign() <= 0 {
		if err != nil {
			s.log().WarnContext(ctx, "fund reinvest price unparseable; skipping",
				"err", err, "fund_id", f.ID, "security_id", c.SecurityID, "price", c.Price)
		}
		return nil
	}
	// Whole-share quantity affordable with the (security-currency)
	// dividend. gross = quantity × price × (yield/4) was computed in the
	// security currency, so divide the same-currency gross by price.
	gross, ok := s.computeDividend(c)
	if !ok {
		return nil
	}
	qty := reinvestQuantity(gross, price)
	if qty <= 0 {
		return nil
	}
	_, err = s.createFundActorOrder(ctx, fundActorOrderInput{
		FundID:     f.ID,
		SecurityID: c.SecurityID,
		AccountID:  f.BankAccountID,
		Quantity:   qty,
		Direction:  domain.DirectionBuy,
		OrderType:  domain.OrderMarket,
		// InitiatorUser empty — the cron owns the call, not a supervisor.
	})
	if err == nil {
		s.log().InfoContext(ctx, "fund dividend reinvested",
			"fund_id", f.ID, "security_id", c.SecurityID, "quantity", qty)
	}
	return err
}
