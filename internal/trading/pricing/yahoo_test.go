package pricing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestYahoo_FetchesAllExpirations(t *testing.T) {
	exp1 := int64(1714694400) // 2024-05-03
	exp2 := int64(1717113600) // 2024-05-31

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasPrefix(r.URL.Path, "/v6/finance/options/AAPL") {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		date := r.URL.Query().Get("date")
		if date == "" {
			// First call: nearest expiry returned in `options`, full list in `expirationDates`.
			fmt.Fprintf(w, `{"optionChain":{"result":[{"underlyingSymbol":"AAPL","expirationDates":[%d,%d],"options":[{"expirationDate":%d,"calls":[{"contractSymbol":"AAPL240503C00150000","strike":150,"lastPrice":12.5,"impliedVolatility":0.3}],"puts":[{"contractSymbol":"AAPL240503P00150000","strike":150,"lastPrice":2.25,"impliedVolatility":0.28}]}]}]}}`, exp1, exp2, exp1)
			return
		}
		if date != "1717113600" {
			t.Errorf("unexpected date param %q", date)
		}
		fmt.Fprintf(w, `{"optionChain":{"result":[{"underlyingSymbol":"AAPL","expirationDates":[%d,%d],"options":[{"expirationDate":%d,"calls":[{"contractSymbol":"AAPL240531C00160000","strike":160,"lastPrice":7.10,"impliedVolatility":0.32}],"puts":[]}]}]}}`, exp1, exp2, exp2)
	}))
	defer srv.Close()

	c := NewYahoo()
	c.BaseURL = srv.URL

	chain, err := c.GetOptionsChain(context.Background(), "aapl")
	if err != nil {
		t.Fatalf("GetOptionsChain: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one per expiry)", calls)
	}
	if len(chain) != 3 {
		t.Fatalf("len(chain) = %d, want 3 (2 from exp1 + 1 from exp2)", len(chain))
	}

	// Spot-check normalization: 12.5 USD → 1250 cents, IV preserved as-is.
	var aaplCall *OptionContract
	for i := range chain {
		if chain[i].ContractSymbol == "AAPL240503C00150000" {
			aaplCall = &chain[i]
			break
		}
	}
	if aaplCall == nil {
		t.Fatal("missing AAPL240503C00150000 in chain")
	}
	if aaplCall.PremiumCents != 1250 {
		t.Errorf("premium = %d, want 1250", aaplCall.PremiumCents)
	}
	if aaplCall.StrikeCents != 15000 {
		t.Errorf("strike = %d, want 15000", aaplCall.StrikeCents)
	}
	if aaplCall.OptionType != "call" {
		t.Errorf("type = %q", aaplCall.OptionType)
	}
	if aaplCall.ImpliedVolatility != 0.3 {
		t.Errorf("iv = %v, want 0.3", aaplCall.ImpliedVolatility)
	}
}

func TestYahoo_UnknownTickerErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"optionChain":{"result":[],"error":{"code":"Not Found","description":"No data found"}}}`))
	}))
	defer srv.Close()

	c := NewYahoo()
	c.BaseURL = srv.URL
	_, err := c.GetOptionsChain(context.Background(), "ZZZZ")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestYahoo_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewYahoo()
	c.BaseURL = srv.URL
	_, err := c.GetOptionsChain(context.Background(), "AAPL")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestYahoo_BadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewYahoo()
	c.BaseURL = srv.URL
	_, err := c.GetOptionsChain(context.Background(), "AAPL")
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrRateLimited) {
		t.Errorf("500 misclassified as %v", err)
	}
}

// Per-expiry transient errors should be tolerated — the chain returns the
// expiries that did succeed rather than aborting the whole fetch.
func TestYahoo_TolerateMissingExpiry(t *testing.T) {
	exp1 := int64(1714694400)
	exp2 := int64(1717113600)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		date := r.URL.Query().Get("date")
		if date == "" {
			fmt.Fprintf(w, `{"optionChain":{"result":[{"underlyingSymbol":"AAPL","expirationDates":[%d,%d],"options":[{"expirationDate":%d,"calls":[{"contractSymbol":"AAPL240503C00150000","strike":150,"lastPrice":12.5,"impliedVolatility":0.3}],"puts":[]}]}]}}`, exp1, exp2, exp1)
			return
		}
		// Second expiry returns a server error.
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewYahoo()
	c.BaseURL = srv.URL
	chain, err := c.GetOptionsChain(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("GetOptionsChain: %v", err)
	}
	if len(chain) != 1 {
		t.Errorf("len(chain) = %d, want 1 (only exp1 succeeded)", len(chain))
	}
}
