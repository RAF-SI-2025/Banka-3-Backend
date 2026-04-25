package pricing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAlphaVantageOverview_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("function"); got != "OVERVIEW" {
			t.Errorf("function = %q, want OVERVIEW", got)
		}
		if got := r.URL.Query().Get("symbol"); got != "MSFT" {
			t.Errorf("symbol = %q, want MSFT", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Symbol":"MSFT","SharesOutstanding":"7430000000","DividendYield":"0.0072"}`))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	o, err := c.GetCompanyOverview(context.Background(), "msft")
	if err != nil {
		t.Fatalf("GetCompanyOverview: %v", err)
	}
	if o.Ticker != "MSFT" {
		t.Errorf("ticker = %q", o.Ticker)
	}
	if o.SharesOutstanding != 7430000000 {
		t.Errorf("shares = %d", o.SharesOutstanding)
	}
	if o.DividendYield != 0.0072 {
		t.Errorf("yield = %v", o.DividendYield)
	}
}

func TestAlphaVantageOverview_NoneDividend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Symbol":"NVDA","SharesOutstanding":"24500000000","DividendYield":"None"}`))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	o, err := c.GetCompanyOverview(context.Background(), "NVDA")
	if err != nil {
		t.Fatalf("GetCompanyOverview: %v", err)
	}
	if o.DividendYield != 0 {
		t.Errorf("yield = %v, want 0 (None)", o.DividendYield)
	}
}

func TestAlphaVantageOverview_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Information":"daily quota exceeded"}`))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	_, err := c.GetCompanyOverview(context.Background(), "MSFT")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestAlphaVantageOverview_UnknownTicker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	_, err := c.GetCompanyOverview(context.Background(), "RAFA")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAlphaVantageOverview_ZeroShares(t *testing.T) {
	// Defensive: AV occasionally returns "0" or "-" for SharesOutstanding on
	// stale tickers. We treat that as not-found to avoid clobbering seed data.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Symbol":"X","SharesOutstanding":"0","DividendYield":"0"}`))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	_, err := c.GetCompanyOverview(context.Background(), "X")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAlphaVantageOverview_MissingKey(t *testing.T) {
	c := NewAlphaVantage("")
	_, err := c.GetCompanyOverview(context.Background(), "MSFT")
	if err == nil {
		t.Fatal("expected error on missing key")
	}
}
