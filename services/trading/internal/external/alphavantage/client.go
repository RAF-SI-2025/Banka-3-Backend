// Package alphavantage is a small client for the Alpha Vantage market-
// data REST API. The spec (Banka2025.pdf p.40, p.42) names it as one of
// the providers for stock quotes + company overview + forex quotes.
//
// The free tier is 25 requests/day and 5/minute; rate-limit responses
// arrive as HTTP 200 with a "Note" or "Information" string envelope, not
// as 429. ErrThrottled distinguishes them so the caller can back off
// without treating a stale-data event as a hard failure.
package alphavantage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrThrottled is returned when Alpha Vantage refuses with a quota note
// instead of data.
var ErrThrottled = errors.New("alphavantage: rate-limited")

// ErrEmpty is returned when the API responds 200 but with no quote
// payload (typical for an unknown symbol or a freshly-IPO'd ticker).
var ErrEmpty = errors.New("alphavantage: empty response")

// Client talks to https://www.alphavantage.co/query.
//
// A nil Client is not usable; New panics if APIKey is empty since the
// upstream returns a non-error "demo" response that quietly leaks into
// the database. The app layer skips wiring this client when the env var
// is unset, surfacing a warning instead.
type Client struct {
	APIKey  string
	BaseURL string // default: https://www.alphavantage.co/query
	HTTP    *http.Client
}

// New returns a client using a 10-second timeout HTTP client.
func New(apiKey string) *Client {
	if apiKey == "" {
		panic("alphavantage: APIKey is required")
	}
	return &Client{
		APIKey:  apiKey,
		BaseURL: "https://www.alphavantage.co/query",
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

// StockQuote is the parsed GLOBAL_QUOTE payload. Decimal fields are
// kept as strings; the trading service stores everything as numeric
// text and downstream math is done in pkg/money.
type StockQuote struct {
	Symbol        string
	Price         string
	Open          string
	High          string
	Low           string
	PreviousClose string
	Change        string
	ChangePercent string
	Volume        int64
	LatestDay     time.Time
}

// CompanyOverview holds the OVERVIEW fields the trading service needs
// (spec p.40 — outstanding shares + dividend yield). The endpoint
// returns dozens of other fields; we deliberately ignore them.
type CompanyOverview struct {
	Symbol            string
	Name              string
	Currency          string
	Exchange          string
	OutstandingShares int64
	DividendYield     string // already a decimal e.g. "0.0052" for 0.52%
}

// FXQuote is the parsed CURRENCY_EXCHANGE_RATE payload. Alpha Vantage
// returns separate bid_price + ask_price columns for forex (unlike
// stocks), so we surface both directly.
type FXQuote struct {
	From         string
	To           string
	ExchangeRate string
	Bid          string
	Ask          string
	UpdatedAt    time.Time
}

// Quote fetches a single stock/ETF quote.
func (c *Client) Quote(ctx context.Context, symbol string) (*StockQuote, error) {
	body, err := c.do(ctx, url.Values{
		"function": {"GLOBAL_QUOTE"},
		"symbol":   {symbol},
	})
	if err != nil {
		return nil, err
	}
	var env struct {
		GlobalQuote map[string]string `json:"Global Quote"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("alphavantage: decode quote: %w", err)
	}
	if len(env.GlobalQuote) == 0 {
		return nil, ErrEmpty
	}
	q := env.GlobalQuote
	out := &StockQuote{
		Symbol:        q["01. symbol"],
		Price:         strings.TrimSpace(q["05. price"]),
		Open:          strings.TrimSpace(q["02. open"]),
		High:          strings.TrimSpace(q["03. high"]),
		Low:           strings.TrimSpace(q["04. low"]),
		PreviousClose: strings.TrimSpace(q["08. previous close"]),
		Change:        strings.TrimSpace(q["09. change"]),
		ChangePercent: strings.TrimSpace(strings.TrimSuffix(q["10. change percent"], "%")),
	}
	if v := q["06. volume"]; v != "" {
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		out.Volume = n
	}
	if v := q["07. latest trading day"]; v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			out.LatestDay = t
		}
	}
	if out.Price == "" {
		return nil, ErrEmpty
	}
	return out, nil
}

// Overview fetches the company overview.
func (c *Client) Overview(ctx context.Context, symbol string) (*CompanyOverview, error) {
	body, err := c.do(ctx, url.Values{
		"function": {"OVERVIEW"},
		"symbol":   {symbol},
	})
	if err != nil {
		return nil, err
	}
	// OVERVIEW returns a flat object; an unknown symbol returns "{}".
	var raw map[string]string
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("alphavantage: decode overview: %w", err)
	}
	if len(raw) == 0 || raw["Symbol"] == "" {
		return nil, ErrEmpty
	}
	out := &CompanyOverview{
		Symbol:        raw["Symbol"],
		Name:          raw["Name"],
		Currency:      raw["Currency"],
		Exchange:      raw["Exchange"],
		DividendYield: strings.TrimSpace(raw["DividendYield"]),
	}
	if v := raw["SharesOutstanding"]; v != "" {
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		out.OutstandingShares = n
	}
	// Alpha Vantage occasionally emits "None" or "-" for dividend yield;
	// normalise so callers don't try to parse it as a number.
	if out.DividendYield == "None" || out.DividendYield == "-" {
		out.DividendYield = ""
	}
	return out, nil
}

// FXQuote fetches a single forex pair quote.
func (c *Client) FXQuote(ctx context.Context, from, to string) (*FXQuote, error) {
	body, err := c.do(ctx, url.Values{
		"function":      {"CURRENCY_EXCHANGE_RATE"},
		"from_currency": {from},
		"to_currency":   {to},
	})
	if err != nil {
		return nil, err
	}
	var env struct {
		Realtime map[string]string `json:"Realtime Currency Exchange Rate"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("alphavantage: decode fx: %w", err)
	}
	if len(env.Realtime) == 0 {
		return nil, ErrEmpty
	}
	r := env.Realtime
	out := &FXQuote{
		From:         r["1. From_Currency Code"],
		To:           r["3. To_Currency Code"],
		ExchangeRate: strings.TrimSpace(r["5. Exchange Rate"]),
		Bid:          strings.TrimSpace(r["8. Bid Price"]),
		Ask:          strings.TrimSpace(r["9. Ask Price"]),
	}
	if v := r["6. Last Refreshed"]; v != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
			out.UpdatedAt = t
		}
	}
	if out.ExchangeRate == "" {
		return nil, ErrEmpty
	}
	return out, nil
}

// do issues the GET, applies the API key, and rejects rate-limit and
// error envelopes.
func (c *Client) do(ctx context.Context, q url.Values) ([]byte, error) {
	q.Set("apikey", c.APIKey)
	base := c.BaseURL
	if base == "" {
		base = "https://www.alphavantage.co/query"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	httpc := c.HTTP
	if httpc == nil {
		httpc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alphavantage: http %d", resp.StatusCode)
	}
	// Read first into a buffer so we can probe for the rate-limit
	// envelope without consuming the JSON body twice.
	const maxBody = 1 << 20
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if len(buf) > maxBody {
				return nil, fmt.Errorf("alphavantage: response too large")
			}
		}
		if err != nil {
			break
		}
	}
	// Probe the envelope before binding to the typed payload. Alpha
	// Vantage uses three different keys for the same condition:
	//   "Note"        — soft rate-limit (5/min exceeded)
	//   "Information" — hard quota note (25/day exceeded)
	//   "Error Message" — bad symbol/parameters
	var env map[string]json.RawMessage
	if err := json.Unmarshal(buf, &env); err == nil {
		if v, ok := env["Note"]; ok {
			return nil, wrapEnvelope(ErrThrottled, v)
		}
		if v, ok := env["Information"]; ok {
			return nil, wrapEnvelope(ErrThrottled, v)
		}
		if v, ok := env["Error Message"]; ok {
			var msg string
			_ = json.Unmarshal(v, &msg)
			return nil, fmt.Errorf("alphavantage: %s", msg)
		}
	}
	return buf, nil
}

func wrapEnvelope(base error, raw json.RawMessage) error {
	var msg string
	_ = json.Unmarshal(raw, &msg)
	if msg == "" {
		return base
	}
	return fmt.Errorf("%w: %s", base, msg)
}
