// Capital-gains tax (spec p.62).
//
// At end of month — and on supervisor demand — we walk every unpaid
// realized_gain and debit 15% of its RSD-equivalent profit from the
// user's trading account into the state's RSD account. The conversion
// goes through the menjačnica without commission (spec p.62) so the
// state lands exactly the RSD amount we computed.
//
// Loss rows (gain_rsd < 0) are clamped to zero per row before summing.
// The simple model in the spec doesn't define carryforward, so a
// loss neither offsets later gains nor gets refunded; the row gets
// flagged taxed=true after the cron so it doesn't keep recurring in
// the unpaid view.
//
// Grouping is per (user_id, account_id) — trading accounts are the
// only meaningful axis for the bank-side debit. The cron may dispatch
// multiple bank.SettleCapitalGainsTax calls for one user when their
// gains are spread across accounts. Each call is idempotent on its
// own op_id; a retry surfaces the existing legs without re-charging.

package service

import (
	"context"
	"math/big"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TaxRate is 15% per spec p.62.
var taxRate = money.MustParse("0.15")

// TaxSettleInput mirrors bank.SettleCapitalGainsTax.
type TaxSettleInput struct {
	AccountID string
	AmountRSD string
	OpID      string
	Purpose   string
}

// TaxSettler is the trading service's view of bank.SettleCapitalGainsTax.
// The app layer wires this; tests inject a stub.
type TaxSettler interface {
	SettleTax(ctx context.Context, in TaxSettleInput) (string, error)
}

// TaxPosition is one supervisor-facing aggregate row.
type TaxPosition struct {
	UserID        string
	UserKind      domain.UserKind
	UnpaidTaxRSD  string
	PaidTaxYTDRSD string
}

// ListTaxPositionsInput narrows the set returned by ListTaxPositions.
type ListTaxPositionsInput struct {
	UserKind domain.UserKind // empty = both client and employee
}

// ListTaxPositions returns one row per user with non-zero unpaid or
// year-to-date paid tax. Supervisor-only — clients don't see the tax
// dashboard.
func (s *Service) ListTaxPositions(ctx context.Context, in ListTaxPositionsInput) ([]*TaxPosition, error) {
	if _, err := s.requireSupervisor(ctx); err != nil {
		return nil, err
	}
	aggs, err := s.Store.ListTaxAggregates(ctx, in.UserKind)
	if err != nil {
		return nil, err
	}
	out := make([]*TaxPosition, 0, len(aggs))
	for _, a := range aggs {
		unpaid, err := money.Parse(a.UnpaidGainRSD)
		if err != nil {
			return nil, apperr.Internal("parse unpaid gain", err)
		}
		paid, err := money.Parse(a.PaidGainYTDRSD)
		if err != nil {
			return nil, apperr.Internal("parse paid gain", err)
		}
		out = append(out, &TaxPosition{
			UserID:        a.UserID,
			UserKind:      a.UserKind,
			UnpaidTaxRSD:  money.FormatAmount(money.Mul(unpaid, taxRate)),
			PaidTaxYTDRSD: money.FormatAmount(money.Mul(paid, taxRate)),
		})
	}
	return out, nil
}

// RunTaxInput drives one cron invocation. Supervisor manual-triggers
// pass UserID to limit the run to one user; the monthly cron leaves it
// empty to walk everyone with positive unpaid gains.
type RunTaxInput struct {
	UserID   string
	UserKind domain.UserKind
}

// RunTaxResult is the aggregate outcome.
type RunTaxResult struct {
	UsersTaxed        int32
	TotalCollectedRSD string
}

// RunTax debits owed tax for the matching users. Internal (cron) and
// supervisor-triggered paths share this entry point — the cron's
// errgroup goroutine attaches an admin principal to the context before
// calling, so requireSupervisor admits both flows.
func (s *Service) RunTax(ctx context.Context, in RunTaxInput) (*RunTaxResult, error) {
	if _, err := s.requireSupervisor(ctx); err != nil {
		return nil, err
	}
	if s.TaxSettler == nil {
		return nil, apperr.FailedPrecondition("bank tax settler not wired")
	}

	// Resolve the candidate set. With UserID set we limit to that one;
	// otherwise we walk every aggregate row with non-zero unpaid.
	type cand struct {
		UserID   string
		UserKind domain.UserKind
	}
	var cands []cand
	if in.UserID != "" {
		cands = append(cands, cand{UserID: in.UserID, UserKind: in.UserKind})
	} else {
		aggs, err := s.Store.ListTaxAggregates(ctx, in.UserKind)
		if err != nil {
			return nil, err
		}
		for _, a := range aggs {
			unpaid, err := money.Parse(a.UnpaidGainRSD)
			if err != nil {
				return nil, apperr.Internal("parse unpaid gain", err)
			}
			if money.IsPositive(unpaid) {
				cands = append(cands, cand{UserID: a.UserID, UserKind: a.UserKind})
			}
		}
	}

	total := big.NewRat(0, 1)
	var taxed int32
	for _, c := range cands {
		collected, err := s.runTaxForUser(ctx, c.UserID, c.UserKind)
		if err != nil {
			s.Log.Warn("tax run failed for user", "user_id", c.UserID, "err", err.Error())
			continue
		}
		if money.IsPositive(collected) {
			taxed++
			total = money.Add(total, collected)
		}
	}
	return &RunTaxResult{
		UsersTaxed:        taxed,
		TotalCollectedRSD: money.FormatAmount(total),
	}, nil
}

// runTaxForUser pulls all unpaid gains for one user, groups by
// account_id, and dispatches one bank-side debit per group. Negative-
// gain rows are still marked taxed=true (consumed) but contribute zero
// to the debit; the simple model has no carryforward.
func (s *Service) runTaxForUser(ctx context.Context, userID string, kind domain.UserKind) (*big.Rat, error) {
	rows, err := s.Store.ListUnpaidGainsForUser(ctx, userID, kind)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return big.NewRat(0, 1), nil
	}

	// Group: account_id → (rowIDs, sum_positive_gain_rsd).
	type group struct {
		ids       []string
		gainSum   *big.Rat
		accountID string
	}
	groups := map[string]*group{}
	allIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		allIDs = append(allIDs, r.ID)
		g, ok := groups[r.AccountID]
		if !ok {
			g = &group{accountID: r.AccountID, gainSum: big.NewRat(0, 1)}
			groups[r.AccountID] = g
		}
		g.ids = append(g.ids, r.ID)
		gain, err := money.Parse(r.GainRSD)
		if err != nil {
			return nil, apperr.Internal("parse gain_rsd", err)
		}
		if money.IsPositive(gain) {
			g.gainSum = money.Add(g.gainSum, gain)
		}
	}

	collected := big.NewRat(0, 1)
	for _, g := range groups {
		taxAmt := money.Mul(g.gainSum, taxRate)
		// Even when taxAmt is zero (loss-only group), we still want the
		// rows marked taxed=true so they don't recur. We just skip the
		// bank call.
		opID := uuid.NewString()
		if money.IsPositive(taxAmt) {
			settledOpID, err := s.TaxSettler.SettleTax(ctx, TaxSettleInput{
				AccountID: g.accountID,
				AmountRSD: money.FormatAmount(taxAmt),
				OpID:      opID,
				Purpose:   "Porez na kapitalni dobitak",
			})
			if err != nil {
				return nil, err
			}
			if settledOpID != "" {
				opID = settledOpID
			}
			collected = money.Add(collected, taxAmt)
		}
		// Mark the rows in this group taxed atomically.
		err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
			return s.Store.MarkRealizedGainsTaxed(ctx, tx, g.ids, opID)
		})
		if err != nil {
			return nil, err
		}
	}
	_ = allIDs
	return collected, nil
}

// TaxCronContext returns a ctx with an admin principal so the
// monthly-cron path passes requireSupervisor without needing a real
// supervisor session. Same idiom as the daily limit reset cron.
func TaxCronContext(ctx context.Context) context.Context {
	return auth.WithPrincipal(ctx, auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffd",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
}

// taxStoreShim keeps the test surface narrow. Real builds pass *store.Store.
type taxStoreShim interface {
	ListTaxAggregates(ctx context.Context, kind domain.UserKind) ([]*store.TaxAggregate, error)
	ListUnpaidGainsForUser(ctx context.Context, userID string, kind domain.UserKind) ([]*domain.RealizedGain, error)
}

var _ taxStoreShim = (*store.Store)(nil)
