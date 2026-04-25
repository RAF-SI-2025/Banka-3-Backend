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

// ExchangeRateClient queries exchangerate-api.com's open endpoint
// (https://open.er-api.com/v6/latest/{base}). Spec p.42 names this provider
// for forex pair pricing. The open endpoint requires no API key and returns
// every quote currency for a given base in one call, which fits the spec's
// 8-currency / 56-pair grid in 8 requests max.
//
// The paid endpoint at https://v6.exchangerate-api.com/v6/{key}/latest/{base}
// has the same response shape. APIKey is optional: when set we route through
// the paid endpoint, otherwise the open one.
type ExchangeRateClient struct {
	APIKey  string
	BaseURL string
	HTTP    httpDoer
}

func NewExchangeRate(apiKey string) *ExchangeRateClient {
	base := "https://open.er-api.com"
	if apiKey != "" {
		base = "https://v6.exchangerate-api.com"
	}
	return &ExchangeRateClient{
		APIKey:  apiKey,
		BaseURL: base,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *ExchangeRateClient) Name() string { return "exchangerate" }

// exchangeRateResp covers both the open and paid endpoint shapes — they share
// the same field names. `error-type` is set on failure (unsupported-code,
// invalid-key, etc.); `result` is "success" or "error".
type exchangeRateResp struct {
	Result    string             `json:"result"`
	BaseCode  string             `json:"base_code"`
	Rates     map[string]float64 `json:"rates"`
	ErrorType string             `json:"error-type"`
}

// GetRates returns the {quote-currency: rate} map for the given base. Rate is
// "how many units of quote per 1 unit of base" — same direction as the
// `forex_pairs.exchange_rate` column.
func (c *ExchangeRateClient) GetRates(ctx context.Context, base string) (map[string]float64, error) {
	base = strings.ToUpper(strings.TrimSpace(base))
	if base == "" {
		return nil, fmt.Errorf("exchangerate: empty base currency")
	}

	var u string
	if c.APIKey != "" {
		u = c.BaseURL + "/v6/" + url.PathEscape(c.APIKey) + "/latest/" + url.PathEscape(base)
	} else {
		u = c.BaseURL + "/v6/latest/" + url.PathEscape(base)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("exchangerate: status %d", resp.StatusCode)
	}

	var body exchangeRateResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.ErrorType != "" {
		// Unsupported / unknown bases come back as 200 with an error envelope.
		// Map the obvious not-found shapes; everything else is surfaced for
		// logs (bad keys, plan-quota, etc).
		switch body.ErrorType {
		case "unsupported-code", "malformed-request", "base-code":
			return nil, ErrNotFound
		case "quota-reached":
			return nil, ErrRateLimited
		default:
			return nil, fmt.Errorf("exchangerate: %s", body.ErrorType)
		}
	}
	if len(body.Rates) == 0 {
		return nil, ErrNotFound
	}
	return body.Rates, nil
}
