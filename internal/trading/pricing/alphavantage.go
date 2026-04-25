package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AlphaVantageClient queries the GLOBAL_QUOTE endpoint
// (https://www.alphavantage.co/documentation/#latestprice). The free tier
// only exposes price/volume/change — no level-1 spread — so we copy
// PriceCents into Ask/Bid. A real spread would require the (paid) realtime
// bulk-quote endpoint, which is out of scope for the dummy data this issue
// targets.
//
// Quirks worth knowing: AV returns 200 OK with a JSON body that contains
// {"Note": "..."} or {"Information": "..."} when you blow past the rate
// limit, and {"Global Quote": {}} (empty map) for unknown tickers. We map
// those to ErrRateLimited and ErrNotFound respectively so the refresher can
// react sensibly.
type AlphaVantageClient struct {
	APIKey  string
	BaseURL string // override for tests; defaults to https://www.alphavantage.co
	HTTP    httpDoer
}

func NewAlphaVantage(apiKey string) *AlphaVantageClient {
	return &AlphaVantageClient{
		APIKey:  apiKey,
		BaseURL: "https://www.alphavantage.co",
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *AlphaVantageClient) Name() string { return "alphavantage" }

// avQuoteResponse maps the GLOBAL_QUOTE shape. AV uses zero-padded numeric
// keys ("01. symbol") which is awkward but stable across years of the API.
type avQuoteResponse struct {
	GlobalQuote map[string]string `json:"Global Quote"`
	Note        string            `json:"Note"`
	Information string            `json:"Information"`
	ErrorMsg    string            `json:"Error Message"`
}

func (c *AlphaVantageClient) GetQuote(ctx context.Context, ticker string) (Quote, error) {
	if c.APIKey == "" {
		return Quote{}, fmt.Errorf("alphavantage: missing API key")
	}
	q := url.Values{}
	q.Set("function", "GLOBAL_QUOTE")
	q.Set("symbol", strings.ToUpper(strings.TrimSpace(ticker)))
	q.Set("apikey", c.APIKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/query?"+q.Encode(), nil)
	if err != nil {
		return Quote{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Quote{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Quote{}, fmt.Errorf("alphavantage: status %d", resp.StatusCode)
	}

	var body avQuoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Quote{}, err
	}
	if body.Note != "" || body.Information != "" {
		return Quote{}, ErrRateLimited
	}
	if body.ErrorMsg != "" {
		return Quote{}, ErrNotFound
	}
	if len(body.GlobalQuote) == 0 {
		return Quote{}, ErrNotFound
	}

	priceStr := body.GlobalQuote["05. price"]
	if priceStr == "" {
		return Quote{}, ErrNotFound
	}
	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		return Quote{}, fmt.Errorf("alphavantage: bad price %q: %w", priceStr, err)
	}
	cents, ok := dollarsToCents(price)
	if !ok {
		return Quote{}, fmt.Errorf("alphavantage: invalid price %v", price)
	}
	return Quote{
		Ticker:     strings.ToUpper(ticker),
		PriceCents: cents,
		AskCents:   cents,
		BidCents:   cents,
		At:         time.Now().UTC(),
	}, nil
}

// DailyBar is a single day of OHLC data from a provider's daily-history
// endpoint. Volume is intentionally omitted — our `listing_daily_price_info`
// volume column is owned by the executor's intraday fills (#184), and the
// provider's reported volume is on a different population (full-market book vs
// our matched book) so mixing them would corrupt the executor's `nextDelay`
// formula.
type DailyBar struct {
	Date       time.Time
	OpenCents  int64
	HighCents  int64
	LowCents   int64
	CloseCents int64
}

// avDailyResponse maps the TIME_SERIES_DAILY shape. Same zero-padded keys as
// GLOBAL_QUOTE; the rate-limit / not-found / error envelope is also identical.
type avDailyResponse struct {
	TimeSeries  map[string]map[string]string `json:"Time Series (Daily)"`
	Note        string                       `json:"Note"`
	Information string                       `json:"Information"`
	ErrorMsg    string                       `json:"Error Message"`
}

// GetDailyHistory hits TIME_SERIES_DAILY for the ticker and returns parsed
// OHLC bars sorted newest-first. We rely on the default `outputsize=compact`
// (last 100 trading days) since the backfiller only persists a recent window
// and we'd be paying bandwidth for nothing on `full` (20+ years).
func (c *AlphaVantageClient) GetDailyHistory(ctx context.Context, ticker string) ([]DailyBar, error) {
	if c.APIKey == "" {
		return nil, fmt.Errorf("alphavantage: missing API key")
	}
	q := url.Values{}
	q.Set("function", "TIME_SERIES_DAILY")
	q.Set("symbol", strings.ToUpper(strings.TrimSpace(ticker)))
	q.Set("apikey", c.APIKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/query?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alphavantage: status %d", resp.StatusCode)
	}

	var body avDailyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Note != "" || body.Information != "" {
		return nil, ErrRateLimited
	}
	if body.ErrorMsg != "" {
		return nil, ErrNotFound
	}
	if len(body.TimeSeries) == 0 {
		return nil, ErrNotFound
	}

	bars := make([]DailyBar, 0, len(body.TimeSeries))
	for dateStr, vals := range body.TimeSeries {
		d, err := time.ParseInLocation("2006-01-02", dateStr, time.UTC)
		if err != nil {
			continue
		}
		open, errOpen := strconv.ParseFloat(vals["1. open"], 64)
		high, errHigh := strconv.ParseFloat(vals["2. high"], 64)
		low, errLow := strconv.ParseFloat(vals["3. low"], 64)
		closeP, errClose := strconv.ParseFloat(vals["4. close"], 64)
		if errOpen != nil || errHigh != nil || errLow != nil || errClose != nil {
			continue
		}
		oc, ok1 := dollarsToCents(open)
		hc, ok2 := dollarsToCents(high)
		lc, ok3 := dollarsToCents(low)
		cc, ok4 := dollarsToCents(closeP)
		if !ok1 || !ok2 || !ok3 || !ok4 {
			continue
		}
		bars = append(bars, DailyBar{Date: d, OpenCents: oc, HighCents: hc, LowCents: lc, CloseCents: cc})
	}
	if len(bars) == 0 {
		return nil, ErrNotFound
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Date.After(bars[j].Date) })
	return bars, nil
}

// CompanyOverview is the trimmed projection of AV's OVERVIEW endpoint we
// actually persist. AV's response carries ~60 fields (sector, industry,
// margins, ratios, ...); we only need the two the spec table on p.40 calls
// out as derived from this source.
type CompanyOverview struct {
	Ticker            string
	SharesOutstanding int64
	// DividendYield is a fraction (0.0072 = 0.72%). AV returns it as a string
	// in that exact form; "None" means the issuer doesn't pay a dividend, in
	// which case we surface 0.
	DividendYield float64
}

// avOverviewResponse is the relevant subset of AV's OVERVIEW shape. The
// envelope (Note/Information/Error Message) matches the other endpoints; the
// not-found case for OVERVIEW is an empty JSON object `{}`, which decodes to
// a zero Symbol.
type avOverviewResponse struct {
	Symbol            string `json:"Symbol"`
	SharesOutstanding string `json:"SharesOutstanding"`
	DividendYield     string `json:"DividendYield"`
	Note              string `json:"Note"`
	Information       string `json:"Information"`
	ErrorMsg          string `json:"Error Message"`
}

// GetCompanyOverview hits function=OVERVIEW for the ticker. Used by the
// weekly metadata syncer (#229). Treats the empty-body and "None"-dividend
// cases as soft skips so dummy seeds (RAFA/RAFB) and non-dividend payers
// don't pollute logs.
func (c *AlphaVantageClient) GetCompanyOverview(ctx context.Context, ticker string) (CompanyOverview, error) {
	if c.APIKey == "" {
		return CompanyOverview{}, fmt.Errorf("alphavantage: missing API key")
	}
	q := url.Values{}
	q.Set("function", "OVERVIEW")
	q.Set("symbol", strings.ToUpper(strings.TrimSpace(ticker)))
	q.Set("apikey", c.APIKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/query?"+q.Encode(), nil)
	if err != nil {
		return CompanyOverview{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return CompanyOverview{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return CompanyOverview{}, fmt.Errorf("alphavantage: status %d", resp.StatusCode)
	}

	var body avOverviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return CompanyOverview{}, err
	}
	if body.Note != "" || body.Information != "" {
		return CompanyOverview{}, ErrRateLimited
	}
	if body.ErrorMsg != "" {
		return CompanyOverview{}, ErrNotFound
	}
	if body.Symbol == "" {
		return CompanyOverview{}, ErrNotFound
	}

	out := CompanyOverview{Ticker: strings.ToUpper(ticker)}
	// SharesOutstanding: required for the field to be useful. Treat unparseable
	// or zero as not-found so we never overwrite a valid seed with garbage.
	shares, err := strconv.ParseInt(strings.TrimSpace(body.SharesOutstanding), 10, 64)
	if err != nil || shares <= 0 {
		return CompanyOverview{}, ErrNotFound
	}
	out.SharesOutstanding = shares
	// DividendYield: "None" or empty means non-dividend payer; surface 0.
	if dy := strings.TrimSpace(body.DividendYield); dy != "" && !strings.EqualFold(dy, "None") {
		v, err := strconv.ParseFloat(dy, 64)
		if err != nil {
			return CompanyOverview{}, fmt.Errorf("alphavantage: bad dividend yield %q: %w", dy, err)
		}
		out.DividendYield = v
	}
	return out, nil
}
