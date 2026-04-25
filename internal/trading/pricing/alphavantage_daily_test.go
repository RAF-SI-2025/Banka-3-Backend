package pricing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const avDailyOK = `{
  "Meta Data": {"2. Symbol": "MSFT"},
  "Time Series (Daily)": {
    "2026-04-23": {"1. open": "410.00", "2. high": "415.00", "3. low": "408.00", "4. close": "412.45", "5. volume": "12345"},
    "2026-04-24": {"1. open": "412.00", "2. high": "418.00", "3. low": "411.50", "4. close": "417.10", "5. volume": "98765"},
    "2026-04-22": {"1. open": "408.00", "2. high": "412.00", "3. low": "405.50", "4. close": "410.00", "5. volume": "55555"}
  }
}`

func TestAlphaVantage_DailyHistory_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("function"); got != "TIME_SERIES_DAILY" {
			t.Errorf("function = %q, want TIME_SERIES_DAILY", got)
		}
		if got := r.URL.Query().Get("symbol"); got != "MSFT" {
			t.Errorf("symbol = %q, want MSFT", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(avDailyOK))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	bars, err := c.GetDailyHistory(context.Background(), "msft")
	if err != nil {
		t.Fatalf("GetDailyHistory: %v", err)
	}
	if len(bars) != 3 {
		t.Fatalf("len(bars) = %d, want 3", len(bars))
	}
	// Newest-first ordering.
	if bars[0].Date.Format("2006-01-02") != "2026-04-24" {
		t.Errorf("bars[0].Date = %s, want 2026-04-24", bars[0].Date.Format("2006-01-02"))
	}
	if bars[2].Date.Format("2006-01-02") != "2026-04-22" {
		t.Errorf("bars[2].Date = %s, want 2026-04-22", bars[2].Date.Format("2006-01-02"))
	}
	if bars[0].CloseCents != 41710 || bars[0].OpenCents != 41200 {
		t.Errorf("bars[0] OHLC mismatch: open=%d close=%d", bars[0].OpenCents, bars[0].CloseCents)
	}
	if bars[0].HighCents != 41800 || bars[0].LowCents != 41150 {
		t.Errorf("bars[0] high/low: %d/%d", bars[0].HighCents, bars[0].LowCents)
	}
}

func TestAlphaVantage_DailyHistory_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Note":"frequency cap reached"}`))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	_, err := c.GetDailyHistory(context.Background(), "MSFT")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestAlphaVantage_DailyHistory_UnknownTicker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// AV returns either an "Error Message" envelope or an empty time series for unknown symbols.
		_, _ = w.Write([]byte(`{"Error Message":"Invalid API call"}`))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	_, err := c.GetDailyHistory(context.Background(), "ZZZZZ")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAlphaVantage_DailyHistory_EmptySeries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Time Series (Daily)":{}}`))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	_, err := c.GetDailyHistory(context.Background(), "ZZZZZ")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAlphaVantage_DailyHistory_MissingKey(t *testing.T) {
	c := NewAlphaVantage("")
	_, err := c.GetDailyHistory(context.Background(), "MSFT")
	if err == nil {
		t.Fatal("expected error on missing key")
	}
}

func TestAlphaVantage_DailyHistory_BadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	_, err := c.GetDailyHistory(context.Background(), "MSFT")
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrRateLimited) {
		t.Errorf("500 misclassified as %v", err)
	}
}
