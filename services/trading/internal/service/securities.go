package service

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
)

// SecurityWithListing is the catalog row returned to the FE: the
// security row, its (optional) listing, and derived margin metrics.
type SecurityWithListing struct {
	Security           *domain.Security
	Listing            *domain.Listing
	MaintenanceMargin  string // spec p.46
	InitialMarginCost  string // 1.1 × maintenance margin (spec p.47)
	MarketCap          string // spec p.40: outstanding_shares × current price (stocks only)
}

// UpsertSecurity admin-creates/updates a security. Validation is
// type-driven so the FE can post a single payload regardless of kind.
func (s *Service) UpsertSecurity(ctx context.Context, in *domain.Security) (*domain.Security, error) {
	if err := s.requirePermission(ctx, permissions.Admin); err != nil {
		return nil, err
	}
	if err := validateSecurity(in); err != nil {
		return nil, err
	}
	return s.Store.UpsertSecurity(ctx, in)
}

// GetSecurity returns one security joined with its listing and the
// derived margin metrics — same envelope ListSecurities/ListListings
// emit, so the FE can hydrate a detail page off a single round trip.
// Options have no listing row (premium lives on the security itself);
// the listing field on the envelope stays nil in that case.
func (s *Service) GetSecurity(ctx context.Context, id string) (*SecurityWithListing, error) {
	if _, err := s.requirePrincipal(ctx); err != nil {
		return nil, err
	}
	sec, err := s.Store.GetSecurity(ctx, id)
	if err != nil {
		return nil, err
	}
	var listing *domain.Listing
	if l, lerr := s.Store.GetListingBySecurityID(ctx, sec.ID); lerr == nil {
		listing = l
	} else {
		var ae *apperr.Error
		if !(errors.As(lerr, &ae) && ae.Kind == apperr.KindNotFound) {
			return nil, lerr
		}
	}
	return s.decorateSecurity(sec, listing), nil
}

// ListSecuritiesInput exposes the catalog filters to the server layer
// without leaking the store struct in handlers.
type ListSecuritiesInput struct {
	Type          domain.SecurityType
	Search        string
	ExchangeMIC   string
	MinSettlement *time.Time
	MaxSettlement *time.Time
	MinPrice      string
	MaxPrice      string
	MinAsk        string
	MaxAsk        string
	MinBid        string
	MaxBid        string
	MinVolume     string
	MaxVolume     string
	SortBy        string
	SortDesc      bool
	Page          int
	PageSize      int
}

// ListSecurities returns the catalog rows matching the filter, joined
// with their listings and decorated with derived margin metrics.
//
// Per spec p.58 clients see only stocks + futures; actuaries see
// everything. We enforce that here so the FE doesn't need to know the
// rule.
func (s *Service) ListSecurities(ctx context.Context, in ListSecuritiesInput) ([]*SecurityWithListing, int64, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, 0, err
	}
	if p.UserKind == "client" {
		// Spec p.58 — clients can't see forex pairs or options.
		switch in.Type {
		case domain.SecurityForex, domain.SecurityOption:
			return []*SecurityWithListing{}, 0, nil
		case "":
			// "all types" client query — fan out to stock+future-only
			// by recursing. (The store can't OR easily on this enum
			// shape; two queries are clearer than a 4-way union.)
			// Carry every filter/sort through to both legs; only the
			// type (and the futures page reset) differ.
			stockIn := in
			stockIn.Type = domain.SecurityStock
			stocks, totalS, err := s.ListSecurities(ctx, stockIn)
			if err != nil {
				return nil, 0, err
			}
			futureIn := in
			futureIn.Type = domain.SecurityFuture
			futureIn.Page = 1
			futures, totalF, err := s.ListSecurities(ctx, futureIn)
			if err != nil {
				return nil, 0, err
			}
			return append(stocks, futures...), totalS + totalF, nil
		}
	}

	secs, total, err := s.Store.ListSecurities(ctx, store.SecurityFilter{
		Type:          in.Type,
		Search:        in.Search,
		ExchangeMIC:   in.ExchangeMIC,
		MinSettlement: in.MinSettlement,
		MaxSettlement: in.MaxSettlement,
		MinPrice:      in.MinPrice,
		MaxPrice:      in.MaxPrice,
		MinAsk:        in.MinAsk,
		MaxAsk:        in.MaxAsk,
		MinBid:        in.MinBid,
		MaxBid:        in.MaxBid,
		MinVolume:     in.MinVolume,
		MaxVolume:     in.MaxVolume,
		SortBy:        in.SortBy,
		SortDesc:      in.SortDesc,
	}, in.Page, in.PageSize)
	if err != nil {
		return nil, 0, err
	}

	out := make([]*SecurityWithListing, 0, len(secs))
	for _, sec := range secs {
		listing, _ := s.Store.GetListingBySecurityID(ctx, sec.ID)
		out = append(out, s.decorateSecurity(sec, listing))
	}
	return out, total, nil
}

// GetOptionChain returns the option chain for a stock, grouped by
// settlement date. Strikes are filtered to a window around the at-the-
// money strike per spec p.59.
func (s *Service) GetOptionChain(ctx context.Context, stockID string, settlement *time.Time, strikesWindow int) (map[time.Time]*OptionChainGroup, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	// Spec p.58: clients can't see options.
	if p.UserKind == "client" {
		return nil, apperr.PermissionDenied("klijent ne može da pristupi opcijama")
	}
	stock, err := s.Store.GetSecurity(ctx, stockID)
	if err != nil {
		return nil, err
	}
	if stock.Type != domain.SecurityStock {
		return nil, apperr.Validation("only stocks have option chains")
	}
	listing, _ := s.Store.GetListingBySecurityID(ctx, stockID)
	sharedPrice := ""
	if listing != nil {
		sharedPrice = listing.Price
	}

	options, err := s.Store.ListOptionsForUnderlying(ctx, stockID, settlement)
	if err != nil {
		return nil, err
	}

	// Lazy chain generation. A stock with no options at all gets its
	// Black-Scholes chain synthesised on first access. The seed writes
	// securities/listings via raw SQL, so a stock added after the
	// trading container's last refresh tick (spec p.43, default 24h)
	// would otherwise have no chain until the next tick or a restart —
	// the symptom QA saw for AMZN/TSLA. Generating on read makes the
	// chain consistent for every stock with no timing dependency; the
	// periodic refresh still recomputes premiums as prices drift. Only
	// when unfiltered (settlement == nil): an empty result under a
	// settlement filter is a legitimately absent expiry, not a missing
	// chain.
	if len(options) == 0 && settlement == nil && s.Options != nil && listing != nil && listing.Price != "" {
		if _, gerr := s.Options.generateForStock(ctx, stock, listing); gerr != nil {
			s.Log.Warn("lazy option-chain generation failed", "stock", stock.Ticker, "err", gerr.Error())
		} else if options, err = s.Store.ListOptionsForUnderlying(ctx, stockID, settlement); err != nil {
			return nil, err
		}
	}

	type strikeKey struct {
		settle time.Time
		strike string
	}
	rowMap := map[strikeKey]*OptionChainRow{}
	groups := map[time.Time]*OptionChainGroup{}

	for _, o := range options {
		if o.SettlementDate == nil {
			continue
		}
		k := strikeKey{settle: *o.SettlementDate, strike: o.StrikePrice}
		row, ok := rowMap[k]
		if !ok {
			row = &OptionChainRow{StrikePrice: o.StrikePrice}
			rowMap[k] = row
			g, ok := groups[*o.SettlementDate]
			if !ok {
				g = &OptionChainGroup{
					SettlementDate: *o.SettlementDate,
					SharedPrice:    sharedPrice,
				}
				groups[*o.SettlementDate] = g
			}
			g.Rows = append(g.Rows, row)
		}
		switch o.OptionType {
		case domain.OptionCall:
			row.Call = o
		case domain.OptionPut:
			row.Put = o
		}
	}

	// Filter each group's rows to a window around the shared price per
	// spec p.59 ("uvek mora da se zna Shared Price ... celu tabelu
	// podeliti na četvrtine").
	if strikesWindow > 0 && sharedPrice != "" {
		shared, err := money.Parse(sharedPrice)
		if err == nil {
			for _, g := range groups {
				g.Rows = filterStrikeWindow(g.Rows, shared, strikesWindow)
			}
		}
	}

	return groups, nil
}

// OptionChainRow groups one strike's call/put pair (spec p.59).
type OptionChainRow struct {
	StrikePrice string
	Call        *domain.Security
	Put         *domain.Security
}

// OptionChainGroup is one settlement date's worth of strike rows.
type OptionChainGroup struct {
	SettlementDate time.Time
	SharedPrice    string
	Rows           []*OptionChainRow
}

// filterStrikeWindow returns the window rows nearest the at-the-money
// strike: window above + window below + the row at-the-money.
func filterStrikeWindow(rows []*OptionChainRow, shared *big.Rat, window int) []*OptionChainRow {
	type rowDist struct {
		row  *OptionChainRow
		dist *big.Rat
		side int // -1 below, 0 at, +1 above
	}
	var below, above []rowDist
	for _, r := range rows {
		strike, err := money.Parse(r.StrikePrice)
		if err != nil {
			continue
		}
		diff := money.Sub(strike, shared)
		switch diff.Sign() {
		case -1:
			d := money.Sub(shared, strike)
			below = append(below, rowDist{row: r, dist: d, side: -1})
		case 0:
			above = append(above, rowDist{row: r, dist: big.NewRat(0, 1), side: 0})
		case 1:
			above = append(above, rowDist{row: r, dist: diff, side: +1})
		}
	}
	pickClosest := func(s []rowDist, n int) []rowDist {
		// stable insertion sort by dist asc
		for i := 1; i < len(s); i++ {
			for j := i; j > 0 && s[j].dist.Cmp(s[j-1].dist) < 0; j-- {
				s[j], s[j-1] = s[j-1], s[j]
			}
		}
		if len(s) > n {
			return s[:n]
		}
		return s
	}
	belowPicked := pickClosest(below, window)
	abovePicked := pickClosest(above, window+1) // include the at-the-money row if any
	merged := append(append([]rowDist{}, belowPicked...), abovePicked...)
	// stable sort by strike asc using big.Rat compare
	for i := 1; i < len(merged); i++ {
		strikeI, _ := money.Parse(merged[i].row.StrikePrice)
		for j := i; j > 0; j-- {
			strikeJ, _ := money.Parse(merged[j-1].row.StrikePrice)
			if strikeI.Cmp(strikeJ) < 0 {
				merged[j], merged[j-1] = merged[j-1], merged[j]
				strikeI = strikeJ
			} else {
				break
			}
		}
	}
	out := make([]*OptionChainRow, 0, len(merged))
	for _, m := range merged {
		out = append(out, m.row)
	}
	return out
}

// decorateSecurity produces the catalog SecurityWithListing carrying
// the spec p.40 market cap (stocks) and spec p.46 derived margin
// metrics. The fallback when no listing is present (forex without
// explicit row, options) reads price / premium directly from the
// security.
func (s *Service) decorateSecurity(sec *domain.Security, l *domain.Listing) *SecurityWithListing {
	out := &SecurityWithListing{Security: sec, Listing: l}
	if sec.Type == domain.SecurityStock && sec.OutstandingShares > 0 && l != nil && l.Price != "" {
		price, err := money.Parse(l.Price)
		if err != nil {
			s.Log.Warn("market cap: listing price parse failed",
				"security_id", sec.ID, "ticker", sec.Ticker, "price", l.Price, "err", err.Error())
		} else {
			shares := new(big.Rat).SetInt64(sec.OutstandingShares)
			out.MarketCap = money.FormatAmount(money.Mul(shares, price))
		}
	}
	mm, ok := computeMaintenanceMargin(sec, l)
	if !ok {
		return out
	}
	out.MaintenanceMargin = money.FormatAmount(mm)
	out.InitialMarginCost = money.FormatAmount(money.Mul(mm, money.MustParse("1.1")))
	return out
}

// computeMaintenanceMargin per spec p.46-48:
//   stock     = 50% × price
//   future    = 10% × contract_size × price
//   forex     = 10% × contract_size × price
//   option    = 50% × contract_size × underlying_price (we approximate
//               with the option's own listing.price when present;
//               callers that need exact behaviour resolve via the
//               underlying stock).
func computeMaintenanceMargin(sec *domain.Security, l *domain.Listing) (*big.Rat, bool) {
	priceStr := ""
	contrStr := "1"
	switch sec.Type {
	case domain.SecurityOption:
		priceStr = sec.Premium
		contrStr = "100"
	default:
		if l != nil {
			priceStr = l.Price
			contrStr = l.ContractSize
		}
	}
	if priceStr == "" {
		return nil, false
	}
	price, err := money.Parse(priceStr)
	if err != nil {
		return nil, false
	}
	contract, err := money.Parse(contrStr)
	if err != nil {
		return nil, false
	}
	switch sec.Type {
	case domain.SecurityStock:
		return money.Mul(price, money.MustParse("0.5")), true
	case domain.SecurityOption:
		return money.Mul(money.Mul(price, contract), money.MustParse("0.5")), true
	default:
		return money.Mul(money.Mul(price, contract), money.MustParse("0.1")), true
	}
}

// validateSecurity enforces per-type required-field rules with
// Serbian-friendly messages.
func validateSecurity(sec *domain.Security) error {
	if sec.Ticker == "" || sec.Name == "" {
		return apperr.Validation("ticker i name su obavezni")
	}
	if !sec.Currency.Supported() {
		return apperr.Validation("nepodržana valuta")
	}
	switch sec.Type {
	case domain.SecurityStock:
		if sec.OutstandingShares <= 0 {
			return apperr.Validation("akcija mora imati outstanding_shares > 0")
		}
	case domain.SecurityFuture:
		if sec.ContractSize == "" {
			return apperr.Validation("future mora imati contract_size")
		}
		if sec.SettlementDate == nil {
			return apperr.Validation("future mora imati settlement_date")
		}
	case domain.SecurityForex:
		if !sec.BaseCurrency.Supported() || !sec.QuoteCurrency.Supported() {
			return apperr.Validation("forex mora imati base_currency i quote_currency")
		}
		if sec.BaseCurrency == sec.QuoteCurrency {
			return apperr.Validation("base i quote moraju biti različiti")
		}
		if sec.ContractSize == "" {
			sec.ContractSize = "1000" // spec p.48 default
		}
	case domain.SecurityOption:
		if sec.UnderlyingSecurityID == "" {
			return apperr.Validation("opcija mora imati underlying_security_id")
		}
		if sec.OptionType != domain.OptionCall && sec.OptionType != domain.OptionPut {
			return apperr.Validation("opcija mora biti CALL ili PUT")
		}
		if sec.StrikePrice == "" || sec.SettlementDate == nil || sec.Premium == "" {
			return apperr.Validation("opcija mora imati strike_price, settlement_date i premium")
		}
	default:
		return apperr.Validation("nepoznat tip hartije")
	}
	if sec.Liquidity != "" {
		switch strings.ToLower(sec.Liquidity) {
		case "high", "medium", "low":
		default:
			return apperr.Validation("liquidity mora biti high/medium/low")
		}
	}
	return nil
}
