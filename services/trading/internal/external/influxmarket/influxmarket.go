// Package influxmarket is a thin InfluxDB v2 client for mirroring
// market data writes off the canonical Postgres store. Ported from
// the main branch's BonusInfluxDB (PR #285) where the bank-monolith
// kept its time-series in Influx instead of relational; on the
// rewrite Postgres is authoritative and Influx is an optional
// side-channel for analytical queries.
//
// Disable by leaving INFLUX_URL / INFLUX_TOKEN / INFLUX_ORG /
// INFLUX_BUCKET unset — NewFromEnv returns a no-op store and every
// caller silently does nothing.
package influxmarket

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
)

const measurement = "listing_daily_price_info"

// Row is the canonical write shape — one daily bar per listing.
type Row struct {
	ListingID string
	Date      time.Time
	Price     string
	Ask       string
	Bid       string
	ChangeAmt string
	Volume    int64
}

// Store is the influxmarket surface. The zero value is not usable;
// use NewFromEnv. Callers should always check Enabled() before doing
// per-row work, since the entire pipeline is a no-op when Influx
// isn't configured.
type Store interface {
	Enabled() bool
	WriteDaily(ctx context.Context, row Row) error
	LatestDaily(ctx context.Context, listingIDs []string) (map[string]Row, error)
	History(ctx context.Context, listingID string, since time.Time) ([]Row, error)
}

type noop struct{}

func (noop) Enabled() bool                         { return false }
func (noop) WriteDaily(context.Context, Row) error { return nil }
func (noop) LatestDaily(context.Context, []string) (map[string]Row, error) {
	return map[string]Row{}, nil
}
func (noop) History(context.Context, string, time.Time) ([]Row, error) { return nil, nil }

// NewFromEnv reads INFLUX_URL / INFLUX_TOKEN / INFLUX_ORG /
// INFLUX_BUCKET. When any are unset the returned store is a no-op.
func NewFromEnv() Store {
	baseURL := strings.TrimSpace(os.Getenv("INFLUX_URL"))
	token := strings.TrimSpace(os.Getenv("INFLUX_TOKEN"))
	org := strings.TrimSpace(os.Getenv("INFLUX_ORG"))
	bucket := strings.TrimSpace(os.Getenv("INFLUX_BUCKET"))
	if baseURL == "" || token == "" || org == "" || bucket == "" {
		return noop{}
	}
	return &influxStore{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		org:     org,
		bucket:  bucket,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

type influxStore struct {
	baseURL string
	token   string
	org     string
	bucket  string
	client  *http.Client
}

func (s *influxStore) Enabled() bool { return true }

func (s *influxStore) WriteDaily(ctx context.Context, r Row) error {
	if r.ListingID == "" {
		return fmt.Errorf("influxmarket: empty listing id")
	}
	// Line protocol: measurement,tag=value field=value timestamp_ns
	line := fmt.Sprintf("%s,listing_id=%s price=%s,ask=%s,bid=%s,change_amt=%s,volume=%di %d",
		measurement, r.ListingID,
		quoteFloat(r.Price), quoteFloat(r.Ask), quoteFloat(r.Bid),
		quoteFloat(r.ChangeAmt), r.Volume, r.Date.UTC().UnixNano())
	u := fmt.Sprintf("%s/api/v2/write?org=%s&bucket=%s&precision=ns",
		s.baseURL, url.QueryEscape(s.org), url.QueryEscape(s.bucket))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(line))
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "influx write request build failed", "err", err, "listing_id", r.ListingID)
		return err
	}
	req.Header.Set("Authorization", "Token "+s.token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := s.client.Do(req)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "influx write request failed", "err", err, "listing_id", r.ListingID)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		logger.From(ctx).ErrorContext(ctx, "influx write rejected",
			"status", resp.StatusCode, "listing_id", r.ListingID, "body", string(body))
		return fmt.Errorf("influxmarket write: %d %s", resp.StatusCode, string(body))
	}
	return nil
}

// LatestDaily returns the most recent row per listing id. Empty
// listingIDs returns an empty map.
func (s *influxStore) LatestDaily(ctx context.Context, listingIDs []string) (map[string]Row, error) {
	if len(listingIDs) == 0 {
		return map[string]Row{}, nil
	}
	pred := make([]string, 0, len(listingIDs))
	for _, id := range listingIDs {
		pred = append(pred, fmt.Sprintf(`r.listing_id == "%s"`, id))
	}
	flux := fmt.Sprintf(`from(bucket: "%s")
  |> range(start: -30d)
  |> filter(fn: (r) => r._measurement == "%s")
  |> filter(fn: (r) => %s)
  |> last()`,
		s.bucket, measurement, strings.Join(pred, " or "))
	rows, err := s.query(ctx, flux)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Row, len(rows))
	for _, r := range rows {
		out[r.ListingID] = r
	}
	return out, nil
}

// History returns all rows for one listing since `since` (UTC), date
// ascending.
func (s *influxStore) History(ctx context.Context, listingID string, since time.Time) ([]Row, error) {
	flux := fmt.Sprintf(`from(bucket: "%s")
  |> range(start: %s)
  |> filter(fn: (r) => r._measurement == "%s" and r.listing_id == "%s")
  |> sort(columns: ["_time"])`,
		s.bucket, since.UTC().Format(time.RFC3339), measurement, listingID)
	return s.query(ctx, flux)
}

// query runs a Flux script and decodes the CSV response into Rows.
func (s *influxStore) query(ctx context.Context, flux string) ([]Row, error) {
	body, _ := json.Marshal(map[string]any{
		"query": flux,
		"type":  "flux",
	})
	u := fmt.Sprintf("%s/api/v2/query?org=%s", s.baseURL, url.QueryEscape(s.org))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "influx query request build failed", "err", err)
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+s.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/csv")
	resp, err := s.client.Do(req)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "influx query request failed", "err", err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		logger.From(ctx).ErrorContext(ctx, "influx query rejected", "status", resp.StatusCode, "body", string(raw))
		return nil, fmt.Errorf("influxmarket query: %d %s", resp.StatusCode, string(raw))
	}
	rows, err := decodeCSV(resp.Body)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "influx query csv decode failed", "err", err)
		return nil, err
	}
	return rows, nil
}

func decodeCSV(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	var out []Row
	headers := map[string]int{}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		// Influx-CSV starts each table with `#group`, `#datatype`, `#default`,
		// then a header row. Reset headers when we see a fresh `#group`.
		if len(rec) == 0 {
			continue
		}
		first := rec[0]
		if strings.HasPrefix(first, "#") {
			if first == "#group" {
				headers = map[string]int{}
			}
			continue
		}
		if len(headers) == 0 {
			for i, h := range rec {
				headers[h] = i
			}
			continue
		}
		field := safeAt(rec, headers["_field"])
		value := safeAt(rec, headers["_value"])
		listingID := safeAt(rec, headers["listing_id"])
		ts := safeAt(rec, headers["_time"])
		if listingID == "" || field == "" {
			continue
		}
		// Merge same-listing rows (one row per (listing, field) in
		// Influx CSV). Find or append.
		var row *Row
		for i := range out {
			if out[i].ListingID == listingID && out[i].Date.Format(time.RFC3339Nano) == ts {
				row = &out[i]
				break
			}
		}
		if row == nil {
			t, _ := time.Parse(time.RFC3339Nano, ts)
			out = append(out, Row{ListingID: listingID, Date: t})
			row = &out[len(out)-1]
		}
		switch field {
		case "price":
			row.Price = value
		case "ask":
			row.Ask = value
		case "bid":
			row.Bid = value
		case "change_amt":
			row.ChangeAmt = value
		case "volume":
			v, _ := strconv.ParseInt(value, 10, 64)
			row.Volume = v
		}
	}
	return out, nil
}

func safeAt(rec []string, idx int) string {
	if idx < 0 || idx >= len(rec) {
		return ""
	}
	return rec[idx]
}

// quoteFloat returns the string as-is if it parses as a number;
// otherwise returns "0" to keep the influx write valid.
func quoteFloat(s string) string {
	if s == "" {
		return "0"
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	return "0"
}
