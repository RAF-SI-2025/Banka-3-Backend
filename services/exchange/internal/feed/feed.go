// Package feed pulls FX rates from a public upstream and upserts them
// into the exchange service's store. It runs as a background goroutine
// next to the gRPC server; failures are logged but never crash the
// service — a stale rate table is preferable to a service outage.
//
// The default fetcher hits open.er-api.com (free, no API key) which
// returns the daily mid rates against a base currency. We base our
// query on RSD and only persist X→RSD pairs (the bank's exchange
// pricing path only consumes that direction; menjačnica list filters
// to RSD as one side anyway).
package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/store"
)

// Fetcher reports the current cost of 1 unit of each foreign currency
// expressed in RSD. RSD itself is omitted.
type Fetcher interface {
	Fetch(ctx context.Context) (map[domain.Currency]float64, error)
}

// Feeder upserts every supported X→RSD pair on Run/Once.
type Feeder struct {
	Fetcher Fetcher
	Store   *store.Store
	Log     *slog.Logger
	// Spread is the symmetric bid/ask offset around the mid rate, e.g.
	// 0.01 → bid = mid·0.99, ask = mid·1.01. The bank's profit comes
	// from commission, not the spread — this exists so the menjačnica
	// list shows two columns instead of one.
	Spread float64
}

// Run blocks until ctx is done, calling Once at the configured
// interval. The first tick fires immediately.
func (f *Feeder) Run(ctx context.Context, interval time.Duration) error {
	if _, err := f.Once(ctx); err != nil {
		f.Log.Warn("fx feed initial fetch failed", "error", err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if _, err := f.Once(ctx); err != nil {
				f.Log.Warn("fx feed tick failed", "error", err)
			}
		}
	}
}

// Once does one fetch + upsert pass and returns the number of rows
// written. Returns the first hard error from the fetch.
func (f *Feeder) Once(ctx context.Context) (int, error) {
	rates, err := f.Fetcher.Fetch(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	written := 0
	for cur, rsdPerUnit := range rates {
		if cur == domain.CurrencyRSD || !cur.Supported() {
			continue
		}
		bid := rsdPerUnit * (1 - f.Spread)
		ask := rsdPerUnit * (1 + f.Spread)
		r := &domain.Rate{
			From: cur,
			To:   domain.CurrencyRSD,
			Bid:  formatRate(bid),
			Ask:  formatRate(ask),
		}
		if _, err := f.Store.UpsertRate(ctx, r); err != nil {
			f.Log.Warn("fx feed upsert failed", "from", cur, "error", err)
			continue
		}
		// Append an append-only history point so the mobile last-month
		// kursna lista accrues over time. A history write failure must
		// not abort the latest-only update — log and carry on.
		if err := f.Store.InsertRateHistory(ctx, r); err != nil {
			f.Log.Warn("fx feed history insert failed", "from", cur, "error", err)
		}
		written++
	}
	f.Log.Info("fx feed updated", "rows", written)
	return written, nil
}

func formatRate(r float64) string {
	return strconv.FormatFloat(r, 'f', 4, 64)
}

// OpenERAPI fetches rates from https://open.er-api.com — free, no
// auth, returns mid rates per base currency. We base on RSD and invert
// to get X→RSD.
type OpenERAPI struct {
	Client  *http.Client
	BaseURL string // default: https://open.er-api.com/v6/latest/RSD
}

type openERResponse struct {
	Result string             `json:"result"`
	Rates  map[string]float64 `json:"rates"`
}

// Fetch implements Fetcher.
func (o *OpenERAPI) Fetch(ctx context.Context) (map[domain.Currency]float64, error) {
	url := o.BaseURL
	if url == "" {
		url = "https://open.er-api.com/v6/latest/RSD"
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream %s returned %d", url, resp.StatusCode)
	}
	var body openERResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Result != "success" {
		return nil, fmt.Errorf("upstream reported result=%q", body.Result)
	}
	out := make(map[domain.Currency]float64, len(body.Rates))
	for code, rsdAsBase := range body.Rates {
		// rsdAsBase is "1 RSD = rsdAsBase <code>". We want "1 <code> in RSD".
		if rsdAsBase <= 0 {
			continue
		}
		out[domain.Currency(code)] = 1 / rsdAsBase
	}
	return out, nil
}
