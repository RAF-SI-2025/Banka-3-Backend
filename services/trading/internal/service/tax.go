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
	"sort"
	"strings"
	"time"

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

// taxNamespace is the deterministic-uuid namespace for capital-gains tax
// op_ids. Stable across runs so a worker crash between SettleTax and
// MarkRealizedGainsTaxed re-derives the same op_id; bank's idempotency
// (migration bank.0011 unique (op_id, leg_index)) makes the retry safe.
var taxNamespace = uuid.MustParse("3e9bdfaa-0a64-4f0a-9d6a-f9c5b7c4e0aa")

// taxOpID derives the deterministic op_id for one tax-settlement call.
// The discriminator is the *set of realized_gains rows* being taxed,
// not just the (account, year-month) pair — two runs in the same
// month with disjoint new gains MUST produce different op_ids,
// otherwise the bank's `(op_id, leg_index)` unique constraint
// silently swallows the second debit while RunTax still reports it
// as collected (the soak suite caught this in c3-multi-round.cy.ts).
//
// The crash-recovery invariant the original deterministic scheme
// gave us is preserved: a retry against the *same* untaxed gain set
// re-derives the same op_id, the bank no-ops, and
// MarkRealizedGainsTaxed converges.
//
// `period` stays in the hash input for auditability — the format
// makes it human-readable when grepping the bank ledger by op_id.
func taxOpID(accountID string, gainIDs []string, period time.Time) string {
	sorted := make([]string, len(gainIDs))
	copy(sorted, gainIDs)
	sort.Strings(sorted)
	payload := accountID + "|" + period.Format("2006-01") + "|" + strings.Join(sorted, ",")
	return uuid.NewSHA1(taxNamespace, []byte(payload)).String()
}

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
	DisplayName   string
	UnpaidTaxRSD  string
	PaidTaxYTDRSD string
}

// ListTaxPositionsInput narrows the set returned by ListTaxPositions.
type ListTaxPositionsInput struct {
	UserKind  domain.UserKind // empty = both client and employee
	NameQuery string          // case-insensitive substring of "first last"
}

// ListTaxPositions returns one row per user with non-zero unpaid or
// year-to-date paid tax. Supervisor-only — clients don't see the tax
// dashboard. display_name is resolved via Users (user-svc); the
// name_query filter is applied after resolution and falls back to a
// UUID substring match when Users is unwired.
func (s *Service) ListTaxPositions(ctx context.Context, in ListTaxPositionsInput) ([]*TaxPosition, error) {
	if _, err := s.requireSupervisor(ctx); err != nil {
		return nil, err
	}
	aggs, err := s.Store.ListTaxAggregates(ctx, in.UserKind)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(in.NameQuery))
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
		name := ""
		if s.Users != nil {
			n, err := s.Users.DisplayName(ctx, a.UserID, a.UserKind)
			if err != nil {
				// Resolution failure shouldn't drop the row from the
				// supervisor view — they still need to see the debt.
				// Log and fall through with an empty name.
				s.Log.Warn("resolve display_name failed", "user_id", a.UserID, "kind", string(a.UserKind), "err", err.Error())
			} else {
				name = n
			}
		}
		if needle != "" {
			haystack := strings.ToLower(name)
			if name == "" {
				haystack = strings.ToLower(a.UserID)
			}
			if !strings.Contains(haystack, needle) {
				continue
			}
		}
		out = append(out, &TaxPosition{
			UserID:        a.UserID,
			UserKind:      a.UserKind,
			DisplayName:   name,
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
//
// Op_ids are deterministic per (account_id, current month). A retry
// after a partial failure (SettleTax committed at the bank but
// MarkRealizedGainsTaxed didn't) re-derives the same op_id; bank
// no-ops, MarkRealizedGainsTaxed converges. No double-charge.
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
	period := s.now()
	for _, g := range groups {
		taxAmt := money.Mul(g.gainSum, taxRate)
		// op_id discriminates on the row-id set so the bank's
		// idempotency (`unique (op_id, leg_index)`) does not silently
		// swallow a second tax run in the same month. Loss-only groups
		// still derive an op_id so MarkRealizedGainsTaxed stamps
		// tax_op_id consistently.
		opID := taxOpID(g.accountID, g.ids, period)
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

// RealizedPnLRow is the supervisor-facing per-sale row. Mirrors the
// proto message but stays in domain-string form so the service layer
// is free of timestamppb/Currency-proto coupling.
type RealizedPnLRow struct {
	ID            string
	SaleAt        time.Time
	SecurityID    string
	Ticker        string
	AccountID     string
	Quantity      int32
	CostBasisAmt  string
	ProceedsAmt   string
	Currency      domain.Currency
	ProfitNative  string
	ProfitRSD     string
	TaxAmountRSD  string
	Taxed         bool
	TaxedAt       *time.Time
	TaxOpID       string
}

// ListRealizedPnLInput drives the supervisor "Realizovani gubici/dobici"
// detail view. user_id is required; from/to clip realized_at when set.
type ListRealizedPnLInput struct {
	UserID   string
	UserKind domain.UserKind
	From     *time.Time
	To       *time.Time
}

// ListRealizedPnL returns one row per closing sell-execution for the
// given user, decorated with the security ticker and the per-row tax
// (15% of max(profit_rsd, 0)). Supervisor-only — the supervisor tax
// dashboard reads this; clients see their own realised P&L through
// the portfolio surface.
func (s *Service) ListRealizedPnL(ctx context.Context, in ListRealizedPnLInput) ([]*RealizedPnLRow, error) {
	if _, err := s.requireSupervisor(ctx); err != nil {
		return nil, err
	}
	if in.UserID == "" {
		return nil, apperr.Validation("user_id is required")
	}
	rows, err := s.Store.ListRealizedGains(ctx, store.RealizedGainFilter{
		UserID:   in.UserID,
		UserKind: in.UserKind,
		From:     in.From,
		To:       in.To,
	})
	if err != nil {
		return nil, err
	}

	// Batch-resolve tickers. Realised-gains rows in a date window
	// usually concentrate on a handful of securities, so a per-id
	// GetSecurity is cheaper than ListSecurities pagination.
	tickerByID := map[string]string{}
	for _, r := range rows {
		if _, seen := tickerByID[r.SecurityID]; seen {
			continue
		}
		sec, err := s.Store.GetSecurity(ctx, r.SecurityID)
		if err != nil {
			// Don't drop the row — supervisor needs to see the gain
			// even when the security record was deleted. Leave ticker
			// empty.
			s.Log.Warn("resolve security ticker failed", "security_id", r.SecurityID, "err", err.Error())
			tickerByID[r.SecurityID] = ""
			continue
		}
		tickerByID[r.SecurityID] = sec.Ticker
	}

	out := make([]*RealizedPnLRow, 0, len(rows))
	for _, r := range rows {
		gainRSD, err := money.Parse(r.GainRSD)
		if err != nil {
			return nil, apperr.Internal("parse gain_rsd", err)
		}
		// Spec p.62: losses don't generate tax under the simple model.
		var taxRSD = big.NewRat(0, 1)
		if money.IsPositive(gainRSD) {
			taxRSD = money.Mul(gainRSD, taxRate)
		}
		out = append(out, &RealizedPnLRow{
			ID:           r.ID,
			SaleAt:       r.RealizedAt,
			SecurityID:   r.SecurityID,
			Ticker:       tickerByID[r.SecurityID],
			AccountID:    r.AccountID,
			Quantity:     r.Quantity,
			CostBasisAmt: r.CostBasisAmt,
			ProceedsAmt:  r.ProceedsAmt,
			Currency:     r.Currency,
			ProfitNative: r.GainNative,
			ProfitRSD:    r.GainRSD,
			TaxAmountRSD: money.FormatAmount(taxRSD),
			Taxed:        r.Taxed,
			TaxedAt:      r.TaxedAt,
			TaxOpID:      r.TaxOpID,
		})
	}
	return out, nil
}
