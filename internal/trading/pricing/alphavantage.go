package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
