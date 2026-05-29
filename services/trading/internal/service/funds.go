// Investment funds (spec p.71-76).
//
// CRUD + decorate
// ===============
// * ListFunds — discovery board with computed total_value/profit columns.
// * GetFund — fund detail + holdings + caller's position when one exists.
// * CreateFund — supervisor mints a fund + a bank-side RSD account in one go.
// * ListFundPositions — caller's positions (or specified user's for
//   supervisors; BankAsClient sentinel for Profit Banke).
// * GetFundPerformance — daily snapshot time series for the FE chart.
// * ListFundTransactions — invest/withdraw audit log.
//
// SAGAs live in fund_invest_saga.go and fund_withdraw_saga.go; the
// kickoff methods (InvestInFund / WithdrawFromFund) prepare the
// payload + transaction-id and call saga.Start.
//
// Unit-pricing model
// ==================
// Each invested RSD buys `amount_rsd / unit_price` units, where
//   unit_price = total_value_rsd / total_units (when total_units > 0).
// First investment mints amount_rsd units at unit_price = 1 RSD (spec
// is silent; this matches the standard mutual-fund mechanic and the
// "ProcenatFonda" intent). Existing positions aren't diluted on a new
// contribution because the new contribution mints fresh units against
// the fund's current value.

package service

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/account"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	tdomain "github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/google/uuid"
)

// BankAsClientOwnerID re-exports the cross-service sentinel for use by
// the rest of the trading service. Defined in pkg/account so bank +
// trading agree on the value without cross-importing internal packages.
const BankAsClientOwnerID = account.BankAsClientOwnerID

// =====================================================================
// Discovery + read paths
// =====================================================================

// DecoratedFund is a fund row enriched with the derived total_value /
// profit / unit_price columns for the discovery list.
type DecoratedFund struct {
	Fund               *tdomain.Fund
	ManagerDisplayName string
	BankAccountNumber  string
	LiquidRSD          string
	HoldingsValueRSD   string
	TotalValueRSD      string
	ProfitRSD          string
	UnitPriceRSD       string
}

// ListFundsInput exposes the filter + sort knobs.
type ListFundsInput struct {
	Status                 string
	ManagerUserID          string
	MinContributionAtLeast string
	MinContributionAtMost  string
	Sort                   string // "name" (default) / "total_value" / "profit" / "minimum_contribution"
	Order                  string // "asc" (default) / "desc"
}

// ListFunds returns the discovery list. Gated on funds.read.*.
func (s *Service) ListFunds(ctx context.Context, in ListFundsInput) ([]*DecoratedFund, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.HasAny(p.Permissions, permissions.Admin,
		permissions.TradingClient, permissions.FundsReadSupervisor) {
		return nil, apperr.PermissionDenied("nedovoljne permisije za fondove")
	}
	rows, err := s.Store.ListFunds(ctx, store.FundFilter{
		Status:                 in.Status,
		ManagerUserID:          in.ManagerUserID,
		MinContributionAtLeast: in.MinContributionAtLeast,
		MinContributionAtMost:  in.MinContributionAtMost,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*DecoratedFund, 0, len(rows))
	for _, f := range rows {
		out = append(out, s.decorateFund(ctx, f))
	}
	sortDecoratedFunds(out, in.Sort, in.Order)
	return out, nil
}

// GetFundResult bundles the decorated fund + holdings + caller's position.
type GetFundResult struct {
	Fund     *DecoratedFund
	Holdings []*HoldingView
	Position *tdomain.FundPosition
	// PositionShare is the caller's position units / fund total_units
	// (percent, 0..100, "0" when the fund has no units yet).
	PositionSharePct  string
	PositionValueRSD  string
	PositionProfitRSD string
}

// HoldingView is one decorated portfolio_holding row on a fund.
type HoldingView struct {
	Holding      *tdomain.Holding
	Security     *tdomain.Security
	CurrentPrice string
	MarketValue  string
	ProfitNative string
	// Spec p.74 fund-detail columns. ChangeAmt + Volume come off the
	// listing; InitialMarginCost is 1.1 × maintenance margin (same
	// derivation as the securities catalog, spec p.46-48).
	ChangeAmt         string
	Volume            int64
	InitialMarginCost string
}

// GetFund returns the fund detail. Clients see public columns; the
// caller's own position when one exists. Supervisors with
// funds.read.supervisor see everything; admin sees everything.
func (s *Service) GetFund(ctx context.Context, id string) (*GetFundResult, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.HasAny(p.Permissions, permissions.Admin,
		permissions.TradingClient, permissions.FundsReadSupervisor) {
		return nil, apperr.PermissionDenied("nedovoljne permisije za fondove")
	}
	f, err := s.Store.GetFund(ctx, id)
	if err != nil {
		return nil, err
	}
	decorated := s.decorateFund(ctx, f)
	holdings, err := s.Store.ListHoldings(ctx, store.HoldingFilter{
		UserID: f.ID, UserKind: tdomain.KindFund,
	})
	if err != nil {
		return nil, err
	}
	views := make([]*HoldingView, 0, len(holdings))
	for _, h := range holdings {
		views = append(views, s.decorateHolding(ctx, h))
	}
	res := &GetFundResult{Fund: decorated, Holdings: views}
	// Caller's own position. Supervisors viewing on behalf of the bank
	// pass on_behalf_client_id elsewhere; here we just attach the
	// caller's own row.
	if pos, err := s.Store.GetFundPosition(ctx, f.ID, p.UserID); err == nil {
		res.Position = pos
		res.PositionSharePct, res.PositionValueRSD, res.PositionProfitRSD = positionDerivations(pos, decorated)
	}
	return res, nil
}

// =====================================================================
// CreateFund
// =====================================================================

// CreateFundInput is the validated payload.
type CreateFundInput struct {
	Name                string
	Description         string
	MinimumContribution string
	// Optional manager override (admin only). Empty → caller is manager.
	ManagerUserID string
}

// CreateFund mints a new fund + its bank-side RSD account.
// Supervisor-only (admin counts). Spec p.74.
func (s *Service) CreateFund(ctx context.Context, in CreateFundInput) (*tdomain.Fund, error) {
	p, err := s.requireSupervisor(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.HasAny(p.Permissions, permissions.Admin, permissions.FundsManageSupervisor) {
		return nil, apperr.PermissionDenied("nedovoljne permisije za upravljanje fondovima")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperr.Validation("naziv fonda je obavezan")
	}
	min, err := money.Parse(in.MinimumContribution)
	if err != nil || !money.IsNonNegative(min) {
		return nil, apperr.Validation("minimalni ulog nije validan iznos")
	}
	manager := in.ManagerUserID
	if manager == "" {
		manager = p.UserID
	} else {
		if !permissions.Has(p.Permissions, permissions.Admin) {
			return nil, apperr.PermissionDenied("samo admin može da odredi upravnika drugog korisnika")
		}
		if _, perr := uuid.Parse(manager); perr != nil {
			return nil, apperr.Validation("upravnik nije validan korisnik")
		}
		// The manager must be a supervisor (spec p.74 "Menadžer –
		// supervizor koji upravlja fondom"). Enforced when the user
		// resolver is wired; on a minimal dev stack without user-svc we
		// fall back to the UUID check only — same graceful-degradation
		// policy the rest of this file uses for s.Users.
		if s.Users != nil {
			perms, perr := s.Users.EmployeePermissions(ctx, manager)
			if perr != nil {
				return nil, apperr.Validation("upravnik fonda nije zaposleni")
			}
			if !permissions.HasAny(perms, permissions.Admin, permissions.FundsManageSupervisor) {
				return nil, apperr.FailedPrecondition("upravnik fonda mora biti supervizor")
			}
		} else {
			s.Log.Warn("CreateFund manager override not validated — user resolver not wired",
				"manager_user_id", manager)
		}
	}
	if s.Reservations == nil {
		return nil, apperr.Internal("bank client not wired", nil)
	}
	accountID, err := s.Reservations.CreateFundAccount(ctx, name, tdomain.CurrencyRSD)
	if err != nil {
		return nil, fmt.Errorf("bank.CreateFundAccount: %w", err)
	}
	f := &tdomain.Fund{
		Name:                name,
		Description:         strings.TrimSpace(in.Description),
		ManagerUserID:       manager,
		BankAccountID:       accountID,
		MinimumContribution: money.FormatAmount(min),
		TotalUnits:          "0",
		Status:              tdomain.FundActive,
	}
	return s.Store.InsertFund(ctx, f)
}

// =====================================================================
// Decoration helpers
// =====================================================================

// decorateFund adds liquid/holdings/total/profit/unit-price computed
// fields. Pulls liquid balance from bank; holdings value from the rate
// provider × portfolio. Falls back gracefully to "0" on dev stacks
// without the upstreams wired.
func (s *Service) decorateFund(ctx context.Context, f *tdomain.Fund) *DecoratedFund {
	d := &DecoratedFund{Fund: f}
	// Liquid RSD = fund.bank_account.balance. Prefer Reservations
	// (same surface SAGAs use) so the discovery list reflects what
	// the next invest/withdraw will see.
	if s.Reservations != nil {
		if _, bal, err := s.Reservations.AccountAvailable(ctx, f.BankAccountID); err == nil {
			d.LiquidRSD = bal
		}
	} else if s.MarginChecker != nil {
		if _, bal, err := s.MarginChecker.AccountAvailable(ctx, f.BankAccountID); err == nil {
			d.LiquidRSD = bal
		}
	}
	if d.LiquidRSD == "" {
		d.LiquidRSD = "0"
	}
	// Holdings value = Σ qty × current_price × FX(currency→RSD).
	hv, _ := s.fundHoldingsValueRSD(ctx, f.ID)
	d.HoldingsValueRSD = hv

	liquid, _ := money.Parse(d.LiquidRSD)
	hvr, _ := money.Parse(d.HoldingsValueRSD)
	total := money.Add(liquid, hvr)
	d.TotalValueRSD = money.FormatAmount(total)

	// Profit = total_value − Σ total_invested_rsd across positions.
	investedStr, err := s.Store.SumPositionsInvestedRSD(ctx, f.ID)
	if err == nil {
		invested, _ := money.Parse(investedStr)
		d.ProfitRSD = money.FormatAmount(money.Sub(total, invested))
	} else {
		d.ProfitRSD = "0"
	}
	// Unit price = total_value / total_units (when units > 0; else 1).
	d.UnitPriceRSD = unitPriceRSD(d.TotalValueRSD, f.TotalUnits)

	if s.Users != nil {
		if name, err := s.Users.DisplayName(ctx, f.ManagerUserID, tdomain.KindEmployee); err == nil {
			d.ManagerDisplayName = name
		}
	}
	// Bank account number — cosmetic for the FE "Račun fonda" row.
	if s.Reservations != nil {
		if num, err := s.Reservations.AccountNumber(ctx, f.BankAccountID); err == nil {
			d.BankAccountNumber = num
		}
	}
	return d
}

// fundHoldingsValueRSD computes the fund's marketable holdings value
// in RSD. Returns "0" on dev stacks without a rate provider or when the
// fund holds nothing.
func (s *Service) fundHoldingsValueRSD(ctx context.Context, fundID string) (string, error) {
	holdings, err := s.Store.ListHoldings(ctx, store.HoldingFilter{
		UserID: fundID, UserKind: tdomain.KindFund,
	})
	if err != nil {
		return "0", err
	}
	total := money.MustParse("0")
	for _, h := range holdings {
		sec, err := s.Store.GetSecurity(ctx, h.SecurityID)
		if err != nil {
			continue
		}
		listing, err := s.Store.GetListingBySecurityID(ctx, sec.ID)
		if err != nil {
			continue
		}
		price, err := money.Parse(listing.Price)
		if err != nil {
			continue
		}
		cs, err := money.Parse(listing.ContractSize)
		if err != nil {
			cs = money.MustParse("1")
		}
		notional := money.Mul(money.Mul(new(big.Rat).SetInt64(int64(h.Quantity)), cs), price)
		// Convert to RSD via rate provider's ASK (no commission — same
		// convention as tradeValueRSD).
		if sec.Currency != tdomain.CurrencyRSD && sec.Currency != "" && s.Rates != nil {
			_, ask, err := s.Rates.Quote(ctx, sec.Currency, tdomain.CurrencyRSD)
			if err == nil {
				if r, err := money.Parse(ask); err == nil {
					notional = money.Mul(notional, r)
				}
			}
		}
		total = money.Add(total, notional)
	}
	return money.FormatAmount(total), nil
}

// decorateHolding adds market value + profit columns to a holding row.
// Mirrors portfolio.decorateHolding (kept separate so future divergence
// stays simple).
func (s *Service) decorateHolding(ctx context.Context, h *tdomain.Holding) *HoldingView {
	v := &HoldingView{Holding: h}
	sec, err := s.Store.GetSecurity(ctx, h.SecurityID)
	if err != nil {
		return v
	}
	v.Security = sec
	listing, err := s.Store.GetListingBySecurityID(ctx, sec.ID)
	if err != nil || listing == nil {
		return v
	}
	v.CurrentPrice = listing.Price
	v.ChangeAmt = listing.ChangeAmt
	v.Volume = listing.Volume
	if mm, ok := computeMaintenanceMargin(sec, listing); ok {
		v.InitialMarginCost = money.FormatAmount(money.Mul(mm, money.MustParse("1.1")))
	}
	price, _ := money.Parse(listing.Price)
	cs, _ := money.Parse(listing.ContractSize)
	if cs == nil || cs.Sign() == 0 {
		cs = money.MustParse("1")
	}
	q := new(big.Rat).SetInt64(int64(h.Quantity))
	mkt := money.Mul(money.Mul(q, cs), price)
	v.MarketValue = money.FormatAmount(mkt)
	if avg, err := money.Parse(h.WeightedAvgPrice); err == nil {
		cost := money.Mul(money.Mul(q, cs), avg)
		v.ProfitNative = money.FormatAmount(money.Sub(mkt, cost))
	}
	return v
}

// unitPriceRSD returns total_value / total_units in formatted RSD;
// returns "1" when total_units == 0 (EDGE-10 — first invest path).
func unitPriceRSD(totalValueRSD, totalUnits string) string {
	tv, err := money.Parse(totalValueRSD)
	if err != nil {
		return "1"
	}
	u, err := money.Parse(totalUnits)
	if err != nil || u.Sign() == 0 {
		return "1"
	}
	r, err := money.Div(tv, u)
	if err != nil {
		return "1"
	}
	return money.FormatAmount(r)
}

// positionDerivations returns (share_pct, current_value_rsd,
// profit_rsd) for a position. share is computed against the fund's
// total_units; current value is units × unit_price; profit is current
// value − total_invested_rsd.
func positionDerivations(p *tdomain.FundPosition, fund *DecoratedFund) (share, currentValue, profit string) {
	if p == nil || fund == nil {
		return "0", "0", "0"
	}
	pu, err := money.Parse(p.Units)
	if err != nil {
		return "0", "0", "0"
	}
	tu, err := money.Parse(fund.Fund.TotalUnits)
	if err != nil || tu.Sign() == 0 {
		return "0", "0", "0"
	}
	hundred := big.NewRat(100, 1)
	frac, _ := money.Div(pu, tu)
	share = money.FormatAmount(money.Mul(frac, hundred))

	up, err := money.Parse(fund.UnitPriceRSD)
	if err != nil {
		return share, "0", "0"
	}
	cv := money.Mul(pu, up)
	currentValue = money.FormatAmount(cv)

	inv, err := money.Parse(p.TotalInvestedRSD)
	if err != nil {
		return share, currentValue, "0"
	}
	profit = money.FormatAmount(money.Sub(cv, inv))
	return share, currentValue, profit
}

// sortDecoratedFunds sorts in-place by the requested column.
func sortDecoratedFunds(funds []*DecoratedFund, sortBy, order string) {
	desc := strings.EqualFold(order, "desc")
	cmpStr := func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }
	cmpRat := func(a, b string) int {
		ra, _ := money.Parse(a)
		rb, _ := money.Parse(b)
		if ra == nil {
			ra = money.MustParse("0")
		}
		if rb == nil {
			rb = money.MustParse("0")
		}
		return ra.Cmp(rb)
	}
	sort.SliceStable(funds, func(i, j int) bool {
		fi := funds[i]
		fj := funds[j]
		var c int
		switch strings.ToLower(sortBy) {
		case "total_value":
			c = cmpRat(fi.TotalValueRSD, fj.TotalValueRSD)
		case "profit":
			c = cmpRat(fi.ProfitRSD, fj.ProfitRSD)
		case "minimum_contribution":
			c = cmpRat(fi.Fund.MinimumContribution, fj.Fund.MinimumContribution)
		default:
			c = cmpStr(fi.Fund.Name, fj.Fund.Name)
		}
		if desc {
			return c > 0
		}
		return c < 0
	})
}

// =====================================================================
// ListFundPositions
// =====================================================================

// ListFundPositionsInput exposes the supervisor filter.
type ListFundPositionsInput struct {
	ClientID string
	Status   string
}

// DecoratedFundPosition bundles a position with the fund row + the
// computed share/current/profit columns.
type DecoratedFundPosition struct {
	Position        *tdomain.FundPosition
	Fund            *tdomain.Fund
	FundName        string
	SharePct        string
	CurrentValueRSD string
	ProfitRSD       string
	// FundTotalValueRSD is the whole fund's value (spec p.75 client
	// "Moji fondovi" — "Vrednost fonda"), distinct from the position's
	// own CurrentValueRSD.
	FundTotalValueRSD string
}

// ListFundPositions returns the caller's positions (default) or a
// specified client's (supervisors/admin only).
func (s *Service) ListFundPositions(ctx context.Context, in ListFundPositionsInput) ([]*DecoratedFundPosition, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.HasAny(p.Permissions, permissions.Admin,
		permissions.TradingClient, permissions.FundsReadSupervisor) {
		return nil, apperr.PermissionDenied("nedovoljne permisije za fondove")
	}
	target := in.ClientID
	supervisor := permissions.HasAny(p.Permissions, permissions.Admin, permissions.FundsReadSupervisor)
	if target == "" {
		target = p.UserID
	} else if !supervisor && target != p.UserID {
		return nil, apperr.PermissionDenied("nedovoljne permisije")
	}
	rows, err := s.Store.ListFundPositions(ctx, store.FundPositionFilter{
		ClientID: target, Status: in.Status,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*DecoratedFundPosition, 0, len(rows))
	for _, pos := range rows {
		f, err := s.Store.GetFund(ctx, pos.FundID)
		if err != nil {
			continue
		}
		dec := s.decorateFund(ctx, f)
		share, value, profit := positionDerivations(pos, dec)
		out = append(out, &DecoratedFundPosition{
			Position:          pos,
			Fund:              f,
			FundName:          f.Name,
			SharePct:          share,
			CurrentValueRSD:   value,
			ProfitRSD:         profit,
			FundTotalValueRSD: dec.TotalValueRSD,
		})
	}
	return out, nil
}

// =====================================================================
// Performance
// =====================================================================

// GetFundPerformance returns the snapshot time series.
func (s *Service) GetFundPerformance(ctx context.Context, fundID string, days int) ([]*tdomain.FundPerformanceSnapshot, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.HasAny(p.Permissions, permissions.Admin,
		permissions.TradingClient, permissions.FundsReadSupervisor) {
		return nil, apperr.PermissionDenied("nedovoljne permisije za fondove")
	}
	if _, err := s.Store.GetFund(ctx, fundID); err != nil {
		return nil, err
	}
	return s.Store.ListFundPerformanceSnapshots(ctx, fundID, days)
}

// SnapshotPerformance writes one snapshot row for `fundID` now. Called
// by the daily cron + an admin-triggered RPC.
func (s *Service) SnapshotPerformance(ctx context.Context, fundID string, now time.Time) error {
	f, err := s.Store.GetFund(ctx, fundID)
	if err != nil {
		return err
	}
	dec := s.decorateFund(ctx, f)
	return s.Store.InsertFundPerformanceSnapshot(ctx, &tdomain.FundPerformanceSnapshot{
		FundID:           f.ID,
		SnapshotAt:       now,
		LiquidRSD:        dec.LiquidRSD,
		HoldingsValueRSD: dec.HoldingsValueRSD,
	})
}

// SnapshotAllFunds walks every active fund and writes one snapshot
// each. Used by the daily cron.
func (s *Service) SnapshotAllFunds(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.Store.ListFunds(ctx, store.FundFilter{Status: "active"})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range rows {
		if err := s.SnapshotPerformance(ctx, f.ID, now); err != nil {
			s.Log.Warn("fund snapshot failed", "fund_id", f.ID, "err", err.Error())
			continue
		}
		n++
	}
	return n, nil
}

// =====================================================================
// Transactions
// =====================================================================

// ListFundTransactionsInput exposes the filter knobs.
type ListFundTransactionsInput struct {
	FundID   string
	ClientID string
	Status   string
	Page     int
	PageSize int
}

// ListFundTransactions returns the audit log scoped to the caller (or
// to a specific client for supervisors/admin).
func (s *Service) ListFundTransactions(ctx context.Context, in ListFundTransactionsInput) ([]*tdomain.FundTransaction, int64, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, 0, err
	}
	if !permissions.HasAny(p.Permissions, permissions.Admin,
		permissions.TradingClient, permissions.FundsReadSupervisor) {
		return nil, 0, apperr.PermissionDenied("nedovoljne permisije")
	}
	supervisor := permissions.HasAny(p.Permissions, permissions.Admin, permissions.FundsReadSupervisor)
	clientID := in.ClientID
	if !supervisor {
		clientID = p.UserID
	}
	return s.Store.ListFundTransactions(ctx, store.FundTransactionFilter{
		FundID:   in.FundID,
		ClientID: clientID,
		Status:   in.Status,
	}, in.Page, in.PageSize)
}

// =====================================================================
// Auth/permission helpers
// =====================================================================

// requireFundManager checks the caller is the fund's manager OR admin.
// Used by both fund-actor order creation and CreateFund cascades.
func requireFundManager(p auth.Principal, f *tdomain.Fund) error {
	if permissions.Has(p.Permissions, permissions.Admin) {
		return nil
	}
	if f.ManagerUserID == p.UserID {
		return nil
	}
	return apperr.PermissionDenied("samo upravnik fonda može da obavlja ovu radnju")
}

// resolveFundInvestor decides whose name the invest/withdraw is being
// done in. Returns (client_id, is_in_name_of_bank, error). Clients
// always invest under their own id; supervisors may pass an
// `on_behalf_client_id` equal to BankAsClientOwnerID to invest in the
// name of the bank.
func resolveFundInvestor(p auth.Principal, onBehalfClientID string) (string, bool, error) {
	if onBehalfClientID == "" {
		// Employees hold no personal fund position — only clients, plus
		// the bank's owner-client sentinel, do (spec p.75 Napomena 2). A
		// supervisor must act in the name of the bank, picking a bank
		// account, so a self-invest by an employee is rejected here.
		if p.UserKind == auth.KindEmployee {
			return "", false, apperr.Validation("supervizor mora delovati u ime banke — izaberite račun banke")
		}
		return p.UserID, false, nil
	}
	supervisor := permissions.HasAny(p.Permissions, permissions.Admin,
		permissions.FundsManageSupervisor, permissions.FundsReadSupervisor)
	if !supervisor {
		return "", false, apperr.PermissionDenied("samo supervizor može da deluje u ime drugog korisnika")
	}
	if onBehalfClientID != BankAsClientOwnerID {
		return "", false, apperr.Validation("on_behalf_client_id mora biti sentinel banke za fond u ime banke")
	}
	return BankAsClientOwnerID, true, nil
}
