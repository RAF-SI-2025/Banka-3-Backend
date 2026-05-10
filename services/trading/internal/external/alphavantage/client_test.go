package alphavantage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleQuote = `{
  "Global Quote": {
    "01. symbol": "MSFT",
    "02. open": "450.00",
    "03. high": "455.20",
    "04. low": "449.10",
    "05. price": "454.30",
    "06. volume": "12345678",
    "07. latest trading day": "2026-05-09",
    "08. previous close": "451.00",
    "09. change": "3.30",
    "10. change percent": "0.7317%"
  }
}`

const sampleOverview = `{
  "Symbol": "MSFT",
  "Name": "Microsoft Corporation",
  "Currency": "USD",
  "Exchange": "NASDAQ",
  "SharesOutstanding": "7430000000",
  "DividendYield": "0.0078"
}`

const sampleFX = `{
  "Realtime Currency Exchange Rate": {
    "1. From_Currency Code": "EUR",
    "2. From_Currency Name": "Euro",
    "3. To_Currency Code": "USD",
    "4. To_Currency Name": "US Dollar",
    "5. Exchange Rate": "1.0850",
    "6. Last Refreshed": "2026-05-09 14:30:00",
    "7. Time Zone": "UTC",
    "8. Bid Price": "1.08495",
    "9. Ask Price": "1.08510"
  }
}`

const noteEnvelope = `{"Note": "Thank you for using Alpha Vantage! Our standard API call frequency is 5 calls per minute"}`
const informationEnvelope = `{"Information": "Daily quota of 25 requests has been reached. Please try again tomorrow."}`
const errorEnvelope = `{"Error Message": "Invalid API call. Please retry or visit the documentation."}`

func newServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") == "" {
			t.Fatalf("expected apikey query parameter")
		}
		_, _ = w.Write([]byte(body))
	}))
}

func newClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c := New("test-key")
	c.BaseURL = baseURL
	return c
}

func TestQuote_ParsesGlobalQuote(t *testing.T) {
	srv := newServer(t, sampleQuote)
	defer srv.Close()
	c := newClient(t, srv.URL)

	q, err := c.Quote(context.Background(), "MSFT")
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if q.Symbol != "MSFT" || q.Price != "454.30" {
		t.Fatalf("unexpected quote: %+v", q)
	}
	if q.Volume != 12345678 {
		t.Fatalf("volume = %d, want 12345678", q.Volume)
	}
	if q.ChangePercent != "0.7317" {
		t.Fatalf("change_percent = %q, want %q (trailing %% stripped)", q.ChangePercent, "0.7317")
	}
	if q.LatestDay.IsZero() {
		t.Fatalf("LatestDay not parsed")
	}
}

func TestOverview_ParsesFields(t *testing.T) {
	srv := newServer(t, sampleOverview)
	defer srv.Close()
	c := newClient(t, srv.URL)

	o, err := c.Overview(context.Background(), "MSFT")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if o.OutstandingShares != 7_430_000_000 {
		t.Fatalf("OutstandingShares = %d", o.OutstandingShares)
	}
	if o.DividendYield != "0.0078" {
		t.Fatalf("DividendYield = %q", o.DividendYield)
	}
}

func TestOverview_NormalisesDashAndNone(t *testing.T) {
	body := strings.Replace(sampleOverview, `"DividendYield": "0.0078"`, `"DividendYield": "None"`, 1)
	srv := newServer(t, body)
	defer srv.Close()
	c := newClient(t, srv.URL)

	o, err := c.Overview(context.Background(), "MSFT")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if o.DividendYield != "" {
		t.Fatalf("DividendYield = %q, want empty", o.DividendYield)
	}
}

func TestFXQuote_ParsesBidAsk(t *testing.T) {
	srv := newServer(t, sampleFX)
	defer srv.Close()
	c := newClient(t, srv.URL)

	q, err := c.FXQuote(context.Background(), "EUR", "USD")
	if err != nil {
		t.Fatalf("FXQuote: %v", err)
	}
	if q.Bid != "1.08495" || q.Ask != "1.08510" || q.ExchangeRate != "1.0850" {
		t.Fatalf("unexpected fx: %+v", q)
	}
	if q.UpdatedAt.IsZero() {
		t.Fatalf("UpdatedAt not parsed")
	}
}

func TestThrottled_NoteEnvelope(t *testing.T) {
	srv := newServer(t, noteEnvelope)
	defer srv.Close()
	c := newClient(t, srv.URL)

	_, err := c.Quote(context.Background(), "MSFT")
	if !errors.Is(err, ErrThrottled) {
		t.Fatalf("err = %v, want ErrThrottled", err)
	}
}

func TestThrottled_InformationEnvelope(t *testing.T) {
	srv := newServer(t, informationEnvelope)
	defer srv.Close()
	c := newClient(t, srv.URL)

	_, err := c.Quote(context.Background(), "MSFT")
	if !errors.Is(err, ErrThrottled) {
		t.Fatalf("err = %v, want ErrThrottled", err)
	}
}

func TestErrorEnvelope_NotThrottled(t *testing.T) {
	srv := newServer(t, errorEnvelope)
	defer srv.Close()
	c := newClient(t, srv.URL)

	_, err := c.Quote(context.Background(), "MSFT")
	if err == nil || errors.Is(err, ErrThrottled) {
		t.Fatalf("err = %v, want non-throttled error", err)
	}
}

func TestEmptyEnvelope_ReturnsErrEmpty(t *testing.T) {
	srv := newServer(t, `{}`)
	defer srv.Close()
	c := newClient(t, srv.URL)

	_, err := c.Quote(context.Background(), "FAKE")
	if !errors.Is(err, ErrEmpty) {
		t.Fatalf("err = %v, want ErrEmpty", err)
	}
}

func TestNew_PanicsWithoutKey(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic when APIKey is empty")
		}
	}()
	_ = New("")
}
