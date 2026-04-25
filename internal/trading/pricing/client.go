// Package pricing wraps the external market-data providers the spec lists for
// the third celina (pp.39–40): Alpha Vantage and Alpaca. Both providers vend
// quotes per ticker; this package surfaces a single Quote shape so callers
// (the refresher in internal/trading) don't care which provider answered.
//
// Prices are returned in int64 minor units (cents/pence/etc) so they slot
// straight into the BIGINT money columns the rest of the system uses.
package pricing

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"
)

// ErrNotFound is returned when the provider has no data for the requested
// ticker. The refresher uses it to skip a listing without aborting the whole
// pass; any other error is treated as a transient outage and surfaces in
// logs.
var ErrNotFound = errors.New("pricing: ticker not found")

// ErrRateLimited signals the provider rejected the request for quota reasons.
// Alpha Vantage's free tier returns 5/min and 25/day; Alpaca free is generous
// but still finite. The refresher backs off when it sees this.
var ErrRateLimited = errors.New("pricing: rate limited")

// Quote is the normalized snapshot we extract from each provider. AskCents
// and BidCents fall back to PriceCents when the provider doesn't expose a
// real spread (Alpha Vantage's GLOBAL_QUOTE is price-only).
type Quote struct {
	Ticker     string
	PriceCents int64
	AskCents   int64
	BidCents   int64
	At         time.Time
}

// Client is what the refresher consumes. Each provider implements it; a
// MultiClient composes several providers with first-success semantics.
type Client interface {
	// Name is used in log lines so an operator can tell which provider
	// served (or refused) a given quote.
	Name() string
	GetQuote(ctx context.Context, ticker string) (Quote, error)
}

// httpDoer is the http.Client subset we depend on, kept narrow so tests can
// stub it without pulling in the full *http.Client surface.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// dollarsToCents converts a provider-reported price (typically a float in
// major units) to int64 minor units. Rounding is half-away-from-zero, which
// matches what most exchanges quote at the tick level. We guard against NaN
// and Inf because both providers occasionally emit them for delisted/halted
// tickers and the int64 cast on those is undefined.
func dollarsToCents(v float64) (int64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	if v < 0 {
		return 0, false
	}
	return int64(math.Round(v * 100)), true
}
