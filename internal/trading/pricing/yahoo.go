package pricing

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

// YahooClient queries the v6/finance/options endpoint
// (https://query1.finance.yahoo.com/v6/finance/options/{ticker}). Spec p.44
// names this as the source for the per-stock option chain — premium and
// implied volatility for each strike/side/expiry. Yahoo's response also
// includes a top-level `expirationDates` slice; a request without `?date=`
// returns only the nearest expiry, so we make follow-up calls for each
// remaining date and stitch the chain together.
//
// Rate limiting: Yahoo's public endpoint is not officially rate-limited but
// will start returning 429 under heavy use. We surface that as
// ErrRateLimited so the per-stock refresher can back off the same way the
// stock-price refresher already does for AV.
type YahooClient struct {
	BaseURL string
	HTTP    httpDoer
}

func NewYahoo() *YahooClient {
	return &YahooClient{
		BaseURL: "https://query1.finance.yahoo.com",
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *YahooClient) Name() string { return "yahoo" }

// OptionContract is the normalized projection of one Yahoo contract row. The
// refresher keys on (OptionType, StrikeCents, SettlementDate) rather than the
// contract symbol because our seed (#197) uses a different ticker format —
// Yahoo's MSFT220404C00180000 vs our MSFT_20220404_00180000_C — and the spec
// (p.43) explicitly notes formats differ across providers.
type OptionContract struct {
	ContractSymbol    string
	OptionType        string // "call" or "put"
	StrikeCents       int64
	PremiumCents      int64
	ImpliedVolatility float64
	SettlementDate    time.Time
}

type yahooOptionsResp struct {
	OptionChain struct {
		Result []yahooOptionResult `json:"result"`
		Error  *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"optionChain"`
}

type yahooOptionResult struct {
	UnderlyingSymbol string             `json:"underlyingSymbol"`
	ExpirationDates  []int64            `json:"expirationDates"`
	Options          []yahooOptionGroup `json:"options"`
}

type yahooOptionGroup struct {
	ExpirationDate int64           `json:"expirationDate"`
	Calls          []yahooContract `json:"calls"`
	Puts           []yahooContract `json:"puts"`
}

type yahooContract struct {
	ContractSymbol    string  `json:"contractSymbol"`
	Strike            float64 `json:"strike"`
	LastPrice         float64 `json:"lastPrice"`
	ImpliedVolatility float64 `json:"impliedVolatility"`
}

// GetOptionsChain fetches every expiration the underlying carries. The first
// request returns the closest expiry plus the full `expirationDates` index;
// the remaining expiries each cost one call. This costs more requests per
// ticker than a stock quote, which is why the options refresher walks at a
// looser cadence than the price refresher (see options_refresher.go).
func (c *YahooClient) GetOptionsChain(ctx context.Context, ticker string) ([]OptionContract, error) {
	first, err := c.fetchChain(ctx, ticker, 0)
	if err != nil {
		return nil, err
	}
	if len(first.OptionChain.Result) == 0 {
		return nil, ErrNotFound
	}
	res := first.OptionChain.Result[0]

	seen := make(map[int64]bool, len(res.ExpirationDates))
	out := make([]OptionContract, 0)
	for _, g := range res.Options {
		seen[g.ExpirationDate] = true
		out = append(out, convertYahooContracts(g.ExpirationDate, g.Calls, "call")...)
		out = append(out, convertYahooContracts(g.ExpirationDate, g.Puts, "put")...)
	}
	for _, exp := range res.ExpirationDates {
		if seen[exp] {
			continue
		}
		body, err := c.fetchChain(ctx, ticker, exp)
		if err != nil {
			// Rate limit short-circuits the whole call so the refresher can
			// abort the tick. Per-expiry NotFound or transient errors are
			// tolerated — a single bad expiry shouldn't lose the rest.
			if errors.Is(err, ErrRateLimited) {
				return nil, err
			}
			continue
		}
		if len(body.OptionChain.Result) == 0 {
			continue
		}
		for _, g := range body.OptionChain.Result[0].Options {
			out = append(out, convertYahooContracts(g.ExpirationDate, g.Calls, "call")...)
			out = append(out, convertYahooContracts(g.ExpirationDate, g.Puts, "put")...)
		}
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

func (c *YahooClient) fetchChain(ctx context.Context, ticker string, date int64) (*yahooOptionsResp, error) {
	u := c.BaseURL + "/v6/finance/options/" + url.PathEscape(strings.ToUpper(strings.TrimSpace(ticker)))
	if date > 0 {
		u += "?date=" + strconv.FormatInt(date, 10)
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
		return nil, fmt.Errorf("yahoo: status %d", resp.StatusCode)
	}

	var body yahooOptionsResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.OptionChain.Error != nil {
		return nil, ErrNotFound
	}
	return &body, nil
}

func convertYahooContracts(exp int64, contracts []yahooContract, kind string) []OptionContract {
	out := make([]OptionContract, 0, len(contracts))
	settle := time.Unix(exp, 0).UTC().Truncate(24 * time.Hour)
	for _, k := range contracts {
		sc, ok := dollarsToCents(k.Strike)
		if !ok {
			continue
		}
		// Premium can legitimately be 0 for deep OTM contracts; dollarsToCents
		// rejects negatives and NaN/Inf only.
		pc, ok := dollarsToCents(k.LastPrice)
		if !ok {
			continue
		}
		out = append(out, OptionContract{
			ContractSymbol:    k.ContractSymbol,
			OptionType:        kind,
			StrikeCents:       sc,
			PremiumCents:      pc,
			ImpliedVolatility: k.ImpliedVolatility,
			SettlementDate:    settle,
		})
	}
	return out
}
