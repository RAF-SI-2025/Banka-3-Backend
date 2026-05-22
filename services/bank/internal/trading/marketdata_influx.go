package trading

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
)

const marketDataMeasurement = "listing_daily_price_info"

type MarketDataStore interface {
	Enabled() bool
	WriteDaily(ctx context.Context, row ListingDailyPriceInfo) error
	LatestDaily(ctx context.Context, listingIDs []int64) (map[int64]ListingDailyPriceInfo, error)
	History(ctx context.Context, listingID int64, since time.Time) ([]ListingDailyPriceInfo, error)
}

type noopMarketDataStore struct{}

func (noopMarketDataStore) Enabled() bool { return false }
func (noopMarketDataStore) WriteDaily(context.Context, ListingDailyPriceInfo) error {
	return nil
}
func (noopMarketDataStore) LatestDaily(context.Context, []int64) (map[int64]ListingDailyPriceInfo, error) {
	return map[int64]ListingDailyPriceInfo{}, nil
}
func (noopMarketDataStore) History(context.Context, int64, time.Time) ([]ListingDailyPriceInfo, error) {
	return nil, nil
}

type influxMarketDataStore struct {
	baseURL string
	token   string
	org     string
	bucket  string
	client  *http.Client
}

func NewMarketDataStoreFromEnv() MarketDataStore {
	baseURL := strings.TrimSpace(os.Getenv("INFLUX_URL"))
	token := strings.TrimSpace(os.Getenv("INFLUX_TOKEN"))
	org := strings.TrimSpace(os.Getenv("INFLUX_ORG"))
	bucket := strings.TrimSpace(os.Getenv("INFLUX_BUCKET"))
	if baseURL == "" || token == "" || org == "" || bucket == "" {
		return noopMarketDataStore{}
	}
	return &influxMarketDataStore{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		org:     org,
		bucket:  bucket,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

func (s *influxMarketDataStore) Enabled() bool { return s != nil }

func (s *influxMarketDataStore) WriteDaily(ctx context.Context, row ListingDailyPriceInfo) error {
	if row.ListingID <= 0 || row.Date.IsZero() {
		return nil
	}
	line := fmt.Sprintf(
		"%s,listing_id=%d price=%di,ask_price=%di,bid_price=%di,change=%di,volume=%di %d",
		marketDataMeasurement,
		row.ListingID,
		row.Price,
		row.AskPrice,
		row.BidPrice,
		row.Change,
		row.Volume,
		row.Date.UTC().UnixNano(),
	)
	endpoint := s.baseURL + "/api/v2/write?org=" + url.QueryEscape(s.org) + "&bucket=" + url.QueryEscape(s.bucket) + "&precision=ns"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(line))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+s.token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("influx write failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
}

func (s *influxMarketDataStore) LatestDaily(ctx context.Context, listingIDs []int64) (map[int64]ListingDailyPriceInfo, error) {
	out := map[int64]ListingDailyPriceInfo{}
	if len(listingIDs) == 0 {
		return out, nil
	}
	set := make([]string, 0, len(listingIDs))
	for _, id := range listingIDs {
		if id > 0 {
			set = append(set, strconv.Quote(strconv.FormatInt(id, 10)))
		}
	}
	if len(set) == 0 {
		return out, nil
	}
	flux := fmt.Sprintf(`
from(bucket: %q)
  |> range(start: time(v: "1970-01-01T00:00:00Z"))
  |> filter(fn: (r) => r._measurement == %q)
  |> filter(fn: (r) => contains(value: r.listing_id, set: [%s]))
  |> filter(fn: (r) => r._field == "price" or r._field == "ask_price" or r._field == "bid_price" or r._field == "change" or r._field == "volume")
  |> group(columns: ["listing_id", "_field"])
  |> last()
`, s.bucket, marketDataMeasurement, strings.Join(set, ","))
	rows, err := s.queryCSV(ctx, flux)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		id, err := strconv.ParseInt(row["listing_id"], 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		rec := out[id]
		rec.ListingID = id
		if ts := strings.TrimSpace(row["_time"]); ts != "" {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				rec.Date = t.UTC()
			}
		}
		val, err := parseInfluxInt(row["_value"])
		if err != nil {
			continue
		}
		switch row["_field"] {
		case "price":
			rec.Price = val
		case "ask_price":
			rec.AskPrice = val
		case "bid_price":
			rec.BidPrice = val
		case "change":
			rec.Change = val
		case "volume":
			rec.Volume = val
		}
		out[id] = rec
	}
	return out, nil
}

func (s *influxMarketDataStore) History(ctx context.Context, listingID int64, since time.Time) ([]ListingDailyPriceInfo, error) {
	if listingID <= 0 {
		return nil, nil
	}
	start := "1970-01-01T00:00:00Z"
	if !since.IsZero() {
		start = since.UTC().Format(time.RFC3339)
	}
	flux := fmt.Sprintf(`
from(bucket: %q)
  |> range(start: time(v: %q))
  |> filter(fn: (r) => r._measurement == %q)
  |> filter(fn: (r) => r.listing_id == %q)
  |> filter(fn: (r) => r._field == "price" or r._field == "ask_price" or r._field == "bid_price" or r._field == "change" or r._field == "volume")
  |> pivot(rowKey: ["_time"], columnKey: ["_field"], valueColumn: "_value")
  |> sort(columns: ["_time"])
`, s.bucket, start, marketDataMeasurement, strconv.FormatInt(listingID, 10))
	rows, err := s.queryCSV(ctx, flux)
	if err != nil {
		return nil, err
	}
	out := make([]ListingDailyPriceInfo, 0, len(rows))
	for _, row := range rows {
		t, err := time.Parse(time.RFC3339, row["_time"])
		if err != nil {
			continue
		}
		price, err1 := parseInfluxInt(row["price"])
		ask, err2 := parseInfluxInt(row["ask_price"])
		bid, err3 := parseInfluxInt(row["bid_price"])
		change, err4 := parseInfluxInt(row["change"])
		volume, err5 := parseInfluxInt(row["volume"])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
			continue
		}
		out = append(out, ListingDailyPriceInfo{
			ListingID: listingID,
			Date:      t.UTC(),
			Price:     price,
			AskPrice:  ask,
			BidPrice:  bid,
			Change:    change,
			Volume:    volume,
		})
	}
	return out, nil
}

func (s *influxMarketDataStore) queryCSV(ctx context.Context, flux string) ([]map[string]string, error) {
	payload, err := json.Marshal(map[string]string{
		"type":  "flux",
		"query": flux,
	})
	if err != nil {
		return nil, err
	}
	endpoint := s.baseURL + "/api/v2/query?org=" + url.QueryEscape(s.org)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Token "+s.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/csv")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("influx query failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	reader := csv.NewReader(resp.Body)
	var header []string
	var rows []map[string]string
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(record) == 0 || strings.HasPrefix(record[0], "#") {
			continue
		}
		if header == nil {
			header = record
			continue
		}
		if len(record) != len(header) {
			continue
		}
		row := make(map[string]string, len(header))
		for i, key := range header {
			row[key] = record[i]
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func parseInfluxInt(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return v, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	return int64(f), nil
}
