package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AlpacaClient queries the v2 market-data REST API
// (https://docs.alpaca.markets/reference/stocklatestquotes-1).
//
// Latest-quote gives ask/bid in one call; we use that as the primary source
// because it carries spread information the executor's fill logic cares
// about. When the response comes back with both sides zero (pre-IPO,
// halted), we treat it as ErrNotFound — surfacing a 0/0/0 listing would
// silently break order matching downstream.
//
// Auth is a pair of headers, not query params, so a leaked URL doesn't
// expose the key.
type AlpacaClient struct {
	KeyID   string
	Secret  string
	BaseURL string // override for tests; defaults to https://data.alpaca.markets
	HTTP    httpDoer
}

func NewAlpaca(keyID, secret string) *AlpacaClient {
	return &AlpacaClient{
		KeyID:   keyID,
		Secret:  secret,
		BaseURL: "https://data.alpaca.markets",
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *AlpacaClient) Name() string { return "alpaca" }

// alpacaLatestQuoteResponse is what /v2/stocks/quotes/latest returns. The
// nested map is keyed by symbol so the same shape powers the bulk endpoint
// if we ever batch — for now we still call the single-symbol path because
// listings get refreshed one at a time.
type alpacaLatestQuoteResponse struct {
	Quotes map[string]struct {
		AskPrice float64 `json:"ap"`
		BidPrice float64 `json:"bp"`
		Time     string  `json:"t"`
	} `json:"quotes"`
}

func (c *AlpacaClient) GetQuote(ctx context.Context, ticker string) (Quote, error) {
	if c.KeyID == "" || c.Secret == "" {
		return Quote{}, fmt.Errorf("alpaca: missing credentials")
	}
	sym := strings.ToUpper(strings.TrimSpace(ticker))

	q := url.Values{}
	q.Set("symbols", sym)
	endpoint := c.BaseURL + "/v2/stocks/quotes/latest?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Quote{}, err
	}
	req.Header.Set("APCA-API-KEY-ID", c.KeyID)
	req.Header.Set("APCA-API-SECRET-KEY", c.Secret)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Quote{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// fallthrough to body parse
	case http.StatusNotFound:
		return Quote{}, ErrNotFound
	case http.StatusTooManyRequests:
		return Quote{}, ErrRateLimited
	default:
		return Quote{}, fmt.Errorf("alpaca: status %d", resp.StatusCode)
	}

	var body alpacaLatestQuoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Quote{}, err
	}
	row, ok := body.Quotes[sym]
	if !ok {
		return Quote{}, ErrNotFound
	}
	if row.AskPrice <= 0 && row.BidPrice <= 0 {
		return Quote{}, ErrNotFound
	}

	// Treat 0 as "not present" rather than "free": Alpaca uses 0 to mean the
	// side has no quote yet (pre-market, halted), and a real $0 listing
	// doesn't exist. Negative or NaN values fall through dollarsToCents.
	ask, askOK := dollarsToCents(row.AskPrice)
	if ask == 0 {
		askOK = false
	}
	bid, bidOK := dollarsToCents(row.BidPrice)
	if bid == 0 {
		bidOK = false
	}
	// Mid-price is the executor's reference for market orders that arrive
	// before any trade has happened today; if only one side is populated we
	// fall back to that side instead of zero so listings.price stays usable.
	var price int64
	switch {
	case askOK && bidOK:
		price = (ask + bid) / 2
	case askOK:
		price = ask
		bid = ask
	case bidOK:
		price = bid
		ask = bid
	default:
		return Quote{}, ErrNotFound
	}

	at := time.Now().UTC()
	if t, err := time.Parse(time.RFC3339Nano, row.Time); err == nil {
		at = t
	}

	return Quote{
		Ticker:     sym,
		PriceCents: price,
		AskCents:   ask,
		BidCents:   bid,
		At:         at,
	}, nil
}
