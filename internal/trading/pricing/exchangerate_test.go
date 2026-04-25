package pricing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExchangeRate_OpenEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v6/latest/USD" {
			t.Errorf("path = %q, want /v6/latest/USD", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"success","base_code":"USD","rates":{"EUR":0.93,"GBP":0.79,"JPY":151.42}}`))
	}))
	defer srv.Close()

	c := NewExchangeRate("")
	c.BaseURL = srv.URL
	rates, err := c.GetRates(context.Background(), "usd")
	if err != nil {
		t.Fatalf("GetRates: %v", err)
	}
	if rates["EUR"] != 0.93 || rates["JPY"] != 151.42 {
		t.Errorf("rates = %v", rates)
	}
}

func TestExchangeRate_PaidEndpointEmbedsKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v6/secret-key/latest/") {
			t.Errorf("path = %q, want it to embed the key", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"result":"success","base_code":"EUR","rates":{"USD":1.07}}`))
	}))
	defer srv.Close()

	c := NewExchangeRate("secret-key")
	c.BaseURL = srv.URL
	rates, err := c.GetRates(context.Background(), "EUR")
	if err != nil {
		t.Fatalf("GetRates: %v", err)
	}
	if rates["USD"] != 1.07 {
		t.Errorf("rates = %v", rates)
	}
}

func TestExchangeRate_UnsupportedCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":"error","error-type":"unsupported-code"}`))
	}))
	defer srv.Close()

	c := NewExchangeRate("")
	c.BaseURL = srv.URL
	_, err := c.GetRates(context.Background(), "XYZ")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestExchangeRate_QuotaReached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":"error","error-type":"quota-reached"}`))
	}))
	defer srv.Close()

	c := NewExchangeRate("k")
	c.BaseURL = srv.URL
	_, err := c.GetRates(context.Background(), "USD")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestExchangeRate_RateLimited429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewExchangeRate("")
	c.BaseURL = srv.URL
	_, err := c.GetRates(context.Background(), "USD")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestExchangeRate_EmptyBase(t *testing.T) {
	c := NewExchangeRate("")
	_, err := c.GetRates(context.Background(), "  ")
	if err == nil {
		t.Fatal("expected error on empty base")
	}
}
