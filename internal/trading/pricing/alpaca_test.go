package pricing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAlpaca_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("APCA-API-KEY-ID") != "key" || r.Header.Get("APCA-API-SECRET-KEY") != "secret" {
			t.Errorf("auth headers missing")
		}
		if got := r.URL.Query().Get("symbols"); got != "AAPL" {
			t.Errorf("symbols = %q, want AAPL", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"quotes":{"AAPL":{"ap":245.51,"bp":245.50,"t":"2025-04-25T13:30:00.123Z"}}}`))
	}))
	defer srv.Close()

	c := NewAlpaca("key", "secret")
	c.BaseURL = srv.URL
	q, err := c.GetQuote(context.Background(), "aapl")
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if q.Ticker != "AAPL" {
		t.Errorf("ticker = %q", q.Ticker)
	}
	// Mid of 24551 + 24550 = 24550 (integer divide).
	if q.PriceCents != 24550 {
		t.Errorf("price = %d, want 24550", q.PriceCents)
	}
	if q.AskCents != 24551 {
		t.Errorf("ask = %d, want 24551", q.AskCents)
	}
	if q.BidCents != 24550 {
		t.Errorf("bid = %d, want 24550", q.BidCents)
	}
	if q.At.IsZero() {
		t.Error("At not parsed")
	}
}

func TestAlpaca_OneSidedQuote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only ask populated — pre-market, illiquid symbol. We should still
		// surface a usable quote rather than fail the refresh.
		_, _ = w.Write([]byte(`{"quotes":{"GOOG":{"ap":174.20,"bp":0,"t":"2025-04-25T08:00:00Z"}}}`))
	}))
	defer srv.Close()

	c := NewAlpaca("key", "secret")
	c.BaseURL = srv.URL
	q, err := c.GetQuote(context.Background(), "GOOG")
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if q.PriceCents != 17420 || q.AskCents != 17420 || q.BidCents != 17420 {
		t.Errorf("price/ask/bid = %d/%d/%d, all want 17420", q.PriceCents, q.AskCents, q.BidCents)
	}
}

func TestAlpaca_NoQuote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Both sides zero — Alpaca does this for halted/delisted tickers.
		_, _ = w.Write([]byte(`{"quotes":{"DEAD":{"ap":0,"bp":0,"t":"2025-04-25T08:00:00Z"}}}`))
	}))
	defer srv.Close()

	c := NewAlpaca("key", "secret")
	c.BaseURL = srv.URL
	_, err := c.GetQuote(context.Background(), "DEAD")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAlpaca_MissingFromMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"quotes":{}}`))
	}))
	defer srv.Close()

	c := NewAlpaca("key", "secret")
	c.BaseURL = srv.URL
	_, err := c.GetQuote(context.Background(), "ZZZZZ")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAlpaca_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewAlpaca("key", "secret")
	c.BaseURL = srv.URL
	_, err := c.GetQuote(context.Background(), "AAPL")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestAlpaca_MissingCreds(t *testing.T) {
	c := NewAlpaca("", "")
	_, err := c.GetQuote(context.Background(), "AAPL")
	if err == nil {
		t.Fatal("expected error on missing credentials")
	}
}
