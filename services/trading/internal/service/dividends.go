// Quarterly dividend payout (todoSpec C3 S54-S59).
//
// On the last business day of each quarter the scheduler calls
// RunDividendPayout. For each stock holding whose security carries a
// positive dividend_yield we compute the per-holding dividend
//
//	dividend = shares × price × (dividend_yield / 4)        (S54)
//
// via pkg/dividend.Quarterly, resolve the destination account, credit it
// through the bank, and record a dividend_payouts row for the portfolio
// history (S59).
//
// Account routing (S54-S56):
//   - the account the shares were bought from (holding.account_id) when
//     it still exists and is in the security's currency (S54);
//   - otherwise the holder's default account in the security's currency
//     (S55);
//   - otherwise an RSD account — the bank converts via the menjačnica
//     (commission-free) and credits RSD (S56).
//
// Tax (S57/S58):
//   - client holders: the dividend is a capital gain, so we write a
//     realized_gains row (gain == proceeds, zero cost basis) and the
//     existing monthly tax cron collects 15% (S57);
//   - actuary holdings "in the name of the bank" (user_kind != client):
//     the dividend goes to Profit Banke (the holding's bank-owned
//     account) and is NOT taxed (S58) — no realized_gains row.
//
// Idempotency: the bank settle op_id is deterministic per
// (holding, quarter), so a retried cron run re-derives the same op_id,
// the bank no-ops, and the dividend_payouts on-conflict insert converges.

package service

import (
	"context"
	"math/big"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/dividend"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// dividendNamespace is the deterministic-uuid namespace for dividend
// payout op_ids. Stable across runs so a worker crash between the bank
// settle and the dividend_payouts insert re-derives the same op_id; the
// bank's idempotency (unique (op_id, leg_index)) makes the retry safe.
var dividendNamespace = uuid.MustParse("b1d4e7c2-9f3a-4c61-8d52-0a7e6b3c1f90")

// dividendOpID derives the deterministic op_id for one holding's payout
// in one quarter. (holding_id, year, quarter) is the discriminator —
// two distinct quarters produce distinct op_ids, but a retry within the
// same quarter re-derives the same one.
func dividendOpID(holdingID string, year, quarter int) string {
	payload := holdingID + "|" + time.Date(year, time.Month(quarter*3), 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
	return uuid.NewSHA1(dividendNamespace, []byte(payload)).String()
}

// RunDividendPayoutResult is the aggregate outcome of one cron run.
type RunDividendPayoutResult struct {
	Paid        int32  // number of holdings credited
	Skipped     int32  // holdings whose computed dividend was zero
	TotalRSD    string // sum of payouts converted to RSD (best-effort)
	RanThisCall bool   // false when the cron no-oped (not quarter-end)
}

// RunDividendPayout pays the quarterly dividend to every eligible
// holder. Internal-only (scheduler cron). It no-ops unless `now` is the
// last business day of its quarter — the scheduler fires it daily and
// this guard makes the quarter-end gate authoritative regardless of the
// cron cadence.
func (s *Service) RunDividendPayout(ctx context.Context) (*RunDividendPayoutResult, error) {
	if s.Reservations == nil {
		return nil, apperr.FailedPrecondition("bank dividend settler not wired")
	}

	now := s.now().In(s.Cfg.Belgrade)
	if !dividend.IsLastBusinessDayOfQuarter(now) {
		s.Log.Info("dividend payout: not quarter-end, skipping", "date", now.Format("2006-01-02"))
		return &RunDividendPayoutResult{RanThisCall: false}, nil
	}

	year := now.Year()
	quarter := dividend.QuarterOf(now)

	cands, err := s.Store.ListDividendCandidates(ctx)
	if err != nil {
		s.log().ErrorContext(ctx, "dividend payout: candidate scan failed", "err", err)
		return nil, err
	}

	res := &RunDividendPayoutResult{RanThisCall: true}
	totalRSD := new(big.Rat)
	for _, c := range cands {
		gross, ok := s.computeDividend(c)
		if !ok {
			res.Skipped++
			continue
		}
		rsd, err := s.payOneDividend(ctx, c, gross, year, quarter)
		if err != nil {
			s.Log.Warn("dividend payout failed for holding",
				"holding_id", c.HoldingID, "user_id", c.UserID, "err", err.Error())
			continue
		}
		res.Paid++
		if rsd != nil {
			totalRSD = money.Add(totalRSD, rsd)
		}
	}
	res.TotalRSD = money.FormatAmount(totalRSD)
	s.Log.Info("dividend payout ran", "paid", res.Paid, "skipped", res.Skipped, "total_rsd", res.TotalRSD)
	return res, nil
}

// computeDividend returns the gross dividend for a candidate via
// pkg/dividend.Quarterly. ok=false when the result is non-positive
// (zero yield/price, or the math rounds to nothing).
func (s *Service) computeDividend(c *store.DividendCandidate) (*big.Rat, bool) {
	price, err := money.Parse(c.Price)
	if err != nil || price.Sign() <= 0 {
		if err != nil {
			s.log().Warn("dividend candidate price unparseable; skipping",
				"err", err, "holding_id", c.HoldingID, "price", c.Price)
		}
		return nil, false
	}
	yield, err := money.Parse(c.DividendYield)
	if err != nil || yield.Sign() <= 0 {
		if err != nil {
			s.log().Warn("dividend candidate yield unparseable; skipping",
				"err", err, "holding_id", c.HoldingID, "yield", c.DividendYield)
		}
		return nil, false
	}
	gross := dividend.Quarterly(int64(c.Quantity), price, yield)
	if gross.Sign() <= 0 {
		return nil, false
	}
	return gross, true
}

// payOneDividend resolves the destination account, credits it via the
// bank, writes the tax row (clients only), and records the payout.
// Returns the payout converted to RSD (best-effort, for the run total).
func (s *Service) payOneDividend(ctx context.Context, c *store.DividendCandidate, gross *big.Rat, year, quarter int) (*big.Rat, error) {
	// Fund-owned holdings take the dedicated fund path (S69-S72): credit
	// the fund's RSD account, attribute across investors, optionally
	// reinvest. The mechanism reuses SettleDividend + the dividend_payouts
	// record (S72); only the routing differs.
	if c.UserKind == domain.KindFund {
		return s.payFundDividend(ctx, c, gross, year, quarter)
	}

	isClient := c.UserKind == domain.KindClient

	destAccountID, destForBank, err := s.resolveDividendAccount(ctx, c, isClient)
	if err != nil {
		return nil, err
	}
	_ = destForBank

	opID := dividendOpID(c.HoldingID, year, quarter)
	grossStr := money.FormatAmount(gross)

	in := DividendSettleInput{
		AccountID: destAccountID,
		Amount:    grossStr,
		Currency:  c.Currency,
		OpID:      opID,
		Purpose:   "Isplata dividende",
	}
	if isClient {
		in.InitiatorClientID = c.UserID
		in.InitiatorClientKind = c.UserKind
	}
	settledOpID, err := s.Reservations.SettleDividend(ctx, in)
	if err != nil {
		return nil, err
	}
	if settledOpID != "" {
		opID = settledOpID
	}

	// Tax: clients pay 15% capital-gains on the dividend (S57); actuary
	// "in the name of the bank" holdings go to Profit Banke untaxed (S58).
	taxRSD := new(big.Rat) // zero
	rsd := s.dividendRSD(ctx, gross, c.Currency)
	if isClient {
		taxRSD = dividend.Tax(rsd)
		if err := s.writeDividendGain(ctx, c, destAccountID, gross, rsd); err != nil {
			return nil, err
		}
	}

	paidAt := s.now()
	if _, err := s.Store.InsertDividendPayout(ctx, &domain.DividendPayout{
		UserID:      c.UserID,
		UserKind:    c.UserKind,
		SecurityID:  c.SecurityID,
		Quantity:    c.Quantity,
		Price:       c.Price,
		GrossAmount: grossStr,
		Currency:    c.Currency,
		AccountID:   destAccountID,
		TaxRSD:      money.FormatAmount(taxRSD),
		OpID:        opID,
		Status:      "paid",
		PaidAt:      &paidAt,
	}); err != nil {
		return nil, err
	}
	return rsd, nil
}

// resolveDividendAccount picks the account to credit (S54-S56).
//
// For an actuary "in the name of the bank" holding (S58) the holding's
// account is already a bank-owned account (the forex_book), so we credit
// it directly — the money stays inside the bank (Profit Banke).
//
// For a client (S54-S56):
//   - if the purchase account still exists and is in the security
//     currency, credit it (S54);
//   - otherwise the holder's first account in the security currency (S55);
//   - otherwise an RSD account — the bank converts on credit (S56).
//
// bankDest is true when the destination is the bank's own account
// (the S58 Profit-Banke path).
func (s *Service) resolveDividendAccount(ctx context.Context, c *store.DividendCandidate, isClient bool) (accountID string, bankDest bool, err error) {
	if !isClient {
		// Actuary in the name of the bank — the holding's account is the
		// bank's forex_book; credit it (Profit Banke, S58).
		return c.AccountID, true, nil
	}

	// S54: purchase account still exists and matches the security
	// currency → credit it directly.
	if c.AccountID != "" {
		cur, _, gerr := s.Reservations.AccountAvailable(ctx, c.AccountID)
		if gerr == nil && cur == c.Currency {
			return c.AccountID, false, nil
		}
		// gerr != nil means the original account is gone (S55) — fall
		// through to the holder's other accounts.
	}

	// S55: another account in the security's currency.
	sameCur, err := s.Reservations.ListClientAccounts(ctx, c.UserID, c.Currency)
	if err != nil {
		return "", false, err
	}
	if len(sameCur) > 0 {
		return sameCur[0].ID, false, nil
	}

	// S56: no account in the security currency — credit an RSD account;
	// the bank converts on credit via the menjačnica (commission-free).
	rsdAccts, err := s.Reservations.ListClientAccounts(ctx, c.UserID, domain.CurrencyRSD)
	if err != nil {
		return "", false, err
	}
	if len(rsdAccts) > 0 {
		return rsdAccts[0].ID, false, nil
	}

	return "", false, apperr.FailedPrecondition("holder nema nijedan račun za isplatu dividende")
}

// dividendRSD converts a dividend in `cur` to RSD via the rate provider's
// ASK (commission-free, spec p.62 reading). Falls back to the native
// amount when the rate provider is unwired or RSD is the currency.
func (s *Service) dividendRSD(ctx context.Context, gross *big.Rat, cur domain.Currency) *big.Rat {
	if cur == "" || cur == domain.CurrencyRSD || s.Rates == nil {
		return new(big.Rat).Set(gross)
	}
	_, ask, err := s.Rates.Quote(ctx, cur, domain.CurrencyRSD)
	if err == nil {
		if r, perr := money.Parse(ask); perr == nil {
			return money.Mul(gross, r)
		}
	}
	s.Log.Warn("rsd conversion for dividend failed; using native value", "currency", cur)
	return new(big.Rat).Set(gross)
}

// writeDividendGain records the dividend as a realized capital gain
// (S57) so the existing monthly tax cron picks it up: full proceeds,
// zero cost basis, so the entire dividend is taxable. AccountID is the
// account the dividend was credited to, so the tax cron debits the
// right account.
func (s *Service) writeDividendGain(ctx context.Context, c *store.DividendCandidate, accountID string, gross, rsd *big.Rat) error {
	grossStr := money.FormatAmount(gross)
	return s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		_, err := s.Store.InsertRealizedGain(ctx, tx, &domain.RealizedGain{
			UserID:       c.UserID,
			UserKind:     c.UserKind,
			SecurityID:   c.SecurityID,
			AccountID:    accountID,
			Quantity:     c.Quantity,
			CostBasisAmt: "0",
			ProceedsAmt:  grossStr,
			Currency:     c.Currency,
			GainNative:   grossStr,
			GainRSD:      money.FormatAmount(rsd),
		})
		return err
	})
}

// ListDividendPayouts returns the caller's dividend history, optionally
// scoped to one security (S59 — per-position view). Visibility mirrors
// ListHoldings: clients/agents see their own; supervisors/admin see
// their own by default and another user's only when an explicit user_id
// is passed.
func (s *Service) ListDividendPayouts(ctx context.Context, in ListDividendPayoutsInput) ([]*domain.DividendPayout, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	userID := p.UserID
	userKind := domain.UserKind(p.UserKind)
	supervisor := permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor)
	if supervisor && in.UserID != "" {
		userID = in.UserID
		if in.UserKind != "" {
			userKind = in.UserKind
		}
	}
	if in.SecurityID != "" {
		return s.Store.ListDividendPayoutsByPosition(ctx, userID, userKind, in.SecurityID)
	}
	return s.Store.ListDividendPayoutsByUser(ctx, userID, userKind)
}

// ListDividendPayoutsInput narrows ListDividendPayouts.
type ListDividendPayoutsInput struct {
	UserID     string
	UserKind   domain.UserKind
	SecurityID string
}
