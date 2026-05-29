package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
)

// OptionGenerator implements spec p.43 Pristup 2: a Black-Scholes
// option chain synthesised against each underlying stock's current
// price. It walks every stock listing, builds the spec's expiry ladder
// (six +6-day rungs followed by six +30-day rungs), the spec's strike
// grid (rounded ATM ± 5 at unit spacing), and upserts CALL+PUT rows
// for every cell. Dedupes on the natural key (ticker, type).
//
// Spec p.43 lets the implementer pick implied volatility "1 ili
// procenjena pomoću specijalizovanih formula"; we go with a more
// realistic 0.30 default (typical equity vol) because IV=1 inflates
// premiums beyond plausibility and breaks the order-form sanity
// checks. Tunable via OPTIONS_VOLATILITY.
type OptionGenerator struct {
	Store *store.Store
	Log   *slog.Logger
	// RiskFreeRate is the continuously-compounded annual rate used in
	// the Black-Scholes discount factor. Defaults to 0.05 (5%).
	RiskFreeRate float64
	// Volatility is the implied volatility we assume for every
	// underlying. Spec p.43 says "1 ili procenjena pomoću
	// specijalizovanih formula"; we default to 0.30 because IV=1 makes
	// short-dated OTM options absurdly expensive.
	Volatility float64
	// Now pins the wall-clock for tests; production leaves it nil.
	Now func() time.Time
	// Belgrade anchors expiry-date rounding. Defaults to Europe/Belgrade.
	Belgrade *time.Location
}

// OptionGeneratorResult summarises one pass.
type OptionGeneratorResult struct {
	UnderlyingsProcessed int
	OptionsUpserted      int
	Skipped              int
}

// RunOnce walks every active stock listing and (re)generates its
// option chain.
func (g *OptionGenerator) RunOnce(ctx context.Context) (*OptionGeneratorResult, error) {
	if g == nil {
		return &OptionGeneratorResult{}, nil
	}
	rows, err := g.allStockListings(ctx)
	if err != nil {
		return nil, err
	}
	res := &OptionGeneratorResult{}
	for _, r := range rows {
		if ctx.Err() != nil {
			return res, nil
		}
		n, err := g.generateForStock(ctx, r.Security, r.Listing)
		if err != nil {
			g.Log.Warn("options generator failed for stock", "ticker", r.Security.Ticker, "err", err.Error())
			res.Skipped++
			continue
		}
		res.UnderlyingsProcessed++
		res.OptionsUpserted += n
	}
	return res, nil
}

func (g *OptionGenerator) allStockListings(ctx context.Context) ([]*store.ListListingsRow, error) {
	var out []*store.ListListingsRow
	for page := 1; ; page++ {
		rows, total, err := g.Store.ListListings(ctx, store.ListingFilter{Type: domain.SecurityStock}, page, 200)
		if err != nil {
			return nil, fmt.Errorf("list stocks: %w", err)
		}
		out = append(out, rows...)
		if int64(len(out)) >= total || len(rows) == 0 {
			break
		}
	}
	return out, nil
}

func (g *OptionGenerator) generateForStock(ctx context.Context, sec *domain.Security, listing *domain.Listing) (int, error) {
	priceRat, err := money.Parse(listing.Price)
	if err != nil {
		return 0, fmt.Errorf("parse price: %w", err)
	}
	priceFloat, _ := priceRat.Float64()
	if priceFloat <= 0 {
		return 0, fmt.Errorf("non-positive price")
	}
	atm := math.Round(priceFloat)

	expiries := g.expiryLadder()
	strikes := strikeGrid(atm)

	r := g.riskFreeRate()
	sigma := g.volatility()
	count := 0
	for _, expiry := range expiries {
		years := yearsTo(g.now(), expiry)
		if years <= 0 {
			continue
		}
		for _, k := range strikes {
			for _, side := range []domain.OptionType{domain.OptionCall, domain.OptionPut} {
				premium := blackScholes(side, priceFloat, k, r, sigma, years)
				if premium < 0 {
					premium = 0
				}
				ticker := optionTicker(sec.Ticker, expiry, side, k)
				_, err := g.Store.UpsertSecurity(ctx, &domain.Security{
					Ticker:               ticker,
					Name:                 fmt.Sprintf("%s %s %s @ %d", sec.Ticker, expiry.Format("2006-01-02"), side, int(k)),
					Type:                 domain.SecurityOption,
					ExchangeMIC:          sec.ExchangeMIC,
					Currency:             sec.Currency,
					ContractSize:         "100",
					SettlementDate:       timePtr(expiry),
					UnderlyingSecurityID: sec.ID,
					OptionType:           side,
					StrikePrice:          formatStrike(k),
					ImpliedVolatility:    formatFloat(sigma, 4),
					Premium:              formatFloat(premium, 4),
					OpenInterest:         0,
				})
				if err != nil {
					return count, fmt.Errorf("upsert option %s: %w", ticker, err)
				}
				count++
			}
		}
	}
	return count, nil
}

// expiryLadder returns the spec p.43 expiry list anchored to today in
// Europe/Belgrade. Six rungs at +6-day intervals starting at +6 (so
// last - first = 30), then six more rungs at +30-day intervals.
func (g *OptionGenerator) expiryLadder() []time.Time {
	loc := g.location()
	today := startOfDay(g.now().In(loc))
	out := make([]time.Time, 0, 12)
	for i := 1; i <= 6; i++ {
		out = append(out, today.AddDate(0, 0, 6*i))
	}
	last := out[len(out)-1]
	for i := 1; i <= 6; i++ {
		out = append(out, last.AddDate(0, 0, 30*i))
	}
	return out
}

// strikeGrid returns 11 strikes: ATM (rounded) ± 5 at unit spacing,
// matching the spec p.43 worked example
// [107…117] for an atm of 112.
func strikeGrid(atm float64) []float64 {
	out := make([]float64, 0, 11)
	for delta := -5; delta <= 5; delta++ {
		k := atm + float64(delta)
		if k <= 0 {
			continue
		}
		out = append(out, k)
	}
	return out
}

// blackScholes returns the European option price for the given side.
// Inputs are spot, strike, risk-free rate (decimal), volatility
// (decimal), and time-to-expiry in years.
func blackScholes(side domain.OptionType, s, k, r, sigma, t float64) float64 {
	if t <= 0 || sigma <= 0 || s <= 0 || k <= 0 {
		// Intrinsic value at/after expiry.
		switch side {
		case domain.OptionCall:
			return math.Max(s-k, 0)
		case domain.OptionPut:
			return math.Max(k-s, 0)
		}
		return 0
	}
	d1 := (math.Log(s/k) + (r+0.5*sigma*sigma)*t) / (sigma * math.Sqrt(t))
	d2 := d1 - sigma*math.Sqrt(t)
	switch side {
	case domain.OptionCall:
		return s*normalCDF(d1) - k*math.Exp(-r*t)*normalCDF(d2)
	case domain.OptionPut:
		return k*math.Exp(-r*t)*normalCDF(-d2) - s*normalCDF(-d1)
	}
	return 0
}

// normalCDF is the standard-normal cumulative distribution function,
// computed via math.Erf.
func normalCDF(x float64) float64 {
	return 0.5 * (1 + math.Erf(x/math.Sqrt2))
}

// optionTicker builds a Yahoo-flavoured ticker that's unique across
// (underlying, expiry, side, strike) so UpsertSecurity's
// (ticker,type) on-conflict idempotently refreshes today's row instead
// of silently overwriting another expiry.
func optionTicker(underlying string, expiry time.Time, side domain.OptionType, strike float64) string {
	letter := "C"
	if side == domain.OptionPut {
		letter = "P"
	}
	return fmt.Sprintf("%s-%s-%s-%d", underlying, expiry.Format("060102"), letter, int(math.Round(strike)))
}

func yearsTo(from, to time.Time) float64 {
	d := to.Sub(from)
	if d <= 0 {
		return 0
	}
	return d.Hours() / (24 * 365.0)
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func timePtr(t time.Time) *time.Time { return &t }

func formatFloat(f float64, scale int) string {
	return strconv.FormatFloat(f, 'f', scale, 64)
}

func formatStrike(k float64) string {
	if k == math.Trunc(k) {
		return strconv.FormatFloat(k, 'f', 1, 64) // "112.0"
	}
	return strconv.FormatFloat(k, 'f', 4, 64)
}

func (g *OptionGenerator) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *OptionGenerator) location() *time.Location {
	if g.Belgrade != nil {
		return g.Belgrade
	}
	loc, err := time.LoadLocation("Europe/Belgrade")
	if err != nil {
		return time.UTC
	}
	return loc
}

func (g *OptionGenerator) riskFreeRate() float64 {
	if g.RiskFreeRate <= 0 {
		return 0.05
	}
	return g.RiskFreeRate
}

func (g *OptionGenerator) volatility() float64 {
	if g.Volatility <= 0 {
		return 0.30
	}
	return g.Volatility
}
