package pricing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAlphaVantage_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("function"); got != "GLOBAL_QUOTE" {
			t.Errorf("function = %q, want GLOBAL_QUOTE", got)
		}
		if got := r.URL.Query().Get("symbol"); got != "MSFT" {
			t.Errorf("symbol = %q, want MSFT", got)
		}
		if got := r.URL.Query().Get("apikey"); got != "demo" {
			t.Errorf("apikey = %q, want demo", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Global Quote":{"01. symbol":"MSFT","05. price":"412.4500","06. volume":"12345"}}`))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	q, err := c.GetQuote(context.Background(), "msft")
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if q.Ticker != "MSFT" {
		t.Errorf("ticker = %q", q.Ticker)
	}
	if q.PriceCents != 41245 {
		t.Errorf("price = %d, want 41245", q.PriceCents)
	}
	// AV doesn't expose a spread on the free tier — both sides should mirror price.
	if q.AskCents != q.PriceCents || q.BidCents != q.PriceCents {
		t.Errorf("ask/bid not mirrored: %d/%d/%d", q.AskCents, q.BidCents, q.PriceCents)
	}
}

func TestAlphaVantage_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Note":"Thank you for using Alpha Vantage! Our standard API call frequency is 5 calls per minute."}`))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	_, err := c.GetQuote(context.Background(), "MSFT")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestAlphaVantage_UnknownTicker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Global Quote":{}}`))
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	_, err := c.GetQuote(context.Background(), "ZZZZZ")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAlphaVantage_MissingKey(t *testing.T) {
	c := NewAlphaVantage("")
	_, err := c.GetQuote(context.Background(), "MSFT")
	if err == nil {
		t.Fatal("expected error on missing key")
	}
}

func TestAlphaVantage_BadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewAlphaVantage("demo")
	c.BaseURL = srv.URL
	_, err := c.GetQuote(context.Background(), "MSFT")
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrRateLimited) {
		t.Errorf("500 misclassified as %v", err)
	}
}
