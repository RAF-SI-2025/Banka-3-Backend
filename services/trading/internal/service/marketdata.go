package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
)

// ErrMarketDataThrottled is the sentinel a provider adapter returns
// when the upstream refuses with a rate-limit / quota envelope. The
// refresher catches it specifically and stops the current pass without
// flagging the error log; production swallows the signal because a
// stale snapshot is preferable to a noisy alert.
var ErrMarketDataThrottled = errors.New("market data: upstream throttled")

// StockQuoteProvider is the bit of an upstream stock-quote feed the
// refresher consumes. The Alpha Vantage GLOBAL_QUOTE adapter returns a
// single mid price (no bid/ask split) plus the change-from-prev-close
// the spec p.40 names directly. Tests inject a stub.
//
// Implementations should return ErrMarketDataThrottled (wrapped or
// equal) when the upstream is rate-limited so the refresher can stop
// the pass cleanly.
type StockQuoteProvider interface {
	Quote(ctx context.Context, symbol string) (price, change string, volume int64, err error)
}

// ForexQuoteProvider is the bid/ask pair from a forex-quote feed
// (Alpha Vantage's CURRENCY_EXCHANGE_RATE returns both columns
// directly, unlike GLOBAL_QUOTE).
type ForexQuoteProvider interface {
	FXQuote(ctx context.Context, from, to string) (bid, ask string, err error)
}

// HistoryBar is one daily close from a stock-history feed. The trading
// service stores a single price per day, so only the close + volume
// are carried; bid/ask are synthesised from StockSpread on persist.
type HistoryBar struct {
	Date   time.Time
	Close  string
	Volume int64
}

// StockHistoryProvider returns the recent daily-close series for a
// symbol, oldest-first. The Alpha Vantage TIME_SERIES_DAILY adapter
// implements it; tests inject a stub. Returns ErrMarketDataThrottled
// (wrapped or equal) on an upstream quota envelope so the backfill can
// stop cleanly, like the live refresher.
type StockHistoryProvider interface {
	DailyHistory(ctx context.Context, symbol string) ([]HistoryBar, error)
}

// MarketData orchestrates one refresh pass across stock + forex
// listings. It walks the catalog, calls the upstream provider for each
// listing, synthesises bid/ask for stocks (where the provider only
// returns a single mid price), and writes both the live-price row and
// the daily-history row.
//
// Spec p.40 names Alpha Vantage's GLOBAL_QUOTE / OVERVIEW for stocks
// and p.42 names Alpha Vantage / Finnhub for forex pairs. Spec p.41
// recommends seed data for futures; p.43 (Pristup 2) lets us generate
// options via Black-Scholes — both handled outside this type.
type MarketData struct {
	Store *store.Store
	Log   *slog.Logger
	// Stocks may be nil; in that case stock listings are skipped.
	Stocks StockQuoteProvider
	// Forex may be nil; in that case forex listings are skipped.
	Forex ForexQuoteProvider
	// History backfills the daily-close chart series for stocks (Alpha
	// Vantage TIME_SERIES_DAILY). May be nil; when nil
	// BackfillStockHistory is a no-op and the keyless synthetic seed
	// remains the chart's only source.
	History StockHistoryProvider
	// StockSpread is the symmetric ±bid/ask offset around the provider's
	// single price for stocks (e.g. 0.001 → bid = price·0.999, ask =
	// price·1.001). The bank's profit on stock trades comes from the
	// commission, not the spread — this exists so the catalog UI can
	// render two columns and so limit-order matching has the structural
	// asymmetry spec p.51 demands.
	StockSpread float64
	// Pause is sleep between provider calls. Alpha Vantage's free tier
	// is 5/min; defaults to 13s when unset.
	Pause time.Duration
	// Now is the wall-clock used to stamp the daily row's date; tests
	// pin it. Production leaves it nil and falls through to time.Now.
	Now func() time.Time
	// Belgrade anchors "today" for the daily history row. Defaults to
	// Europe/Belgrade.
	Belgrade *time.Location
	// Influx, when non-nil, mirrors every successful UpsertListingDaily
	// to InfluxDB as a side-channel for analytical queries. Bonus
	// feature ported from main (#285). Postgres remains the canonical
	// source of truth — Influx errors are logged at warn level but
	// don't fail the refresh.
	Influx MarketDataMirror
}

// MarketDataMirror is the slim Influx surface MarketData uses for its
// side-channel writes. influxmarket.Store implements it; tests can
// fake.
type MarketDataMirror interface {
	Enabled() bool
	WriteDaily(ctx context.Context, listingID string, date time.Time, price, ask, bid, changeAmt string, volume int64) error
}

// MarketDataResult summarises one refresh pass.
type MarketDataResult struct {
	StocksUpdated     int
	ForexUpdated      int
	Skipped           int
	UpstreamThrottled bool
	UpstreamErrors    int
}

// RunOnce walks every active stock + forex listing exactly once. On
// upstream throttling it stops the current pass cleanly so the cron
// keeps whatever it managed to fetch before the quota was exhausted.
func (m *MarketData) RunOnce(ctx context.Context) (*MarketDataResult, error) {
	if m == nil {
		return &MarketDataResult{}, nil
	}
	res := &MarketDataResult{}
	if err := m.refreshStocks(ctx, res); err != nil {
		return res, err
	}
	if err := m.refreshForex(ctx, res); err != nil {
		return res, err
	}
	return res, nil
}

// StockHistoryBackfillResult summarises one backfill pass.
type StockHistoryBackfillResult struct {
	SymbolsBackfilled int
	RowsWritten       int
	Skipped           int
	UpstreamThrottled bool
	UpstreamErrors    int
}

// BackfillStockHistory pulls the recent daily-close series for every
// stock listing and writes it into listing_daily_price_info so the
// listing-detail chart has real history whenever an Alpha Vantage key
// is configured (spec p.40). UpsertListingDaily is an upsert, so this
// overwrites whatever the keyless synthetic seed planted — matching the
// old codebase's "synthetic seed for keyless dev, real history when
// keyed" split.
//
// A symbol that already has a row stamped today is skipped: the daily
// series doesn't change intraday, so re-running on every container
// restart would burn the free-tier quota for no new data. On an
// upstream quota envelope the pass stops cleanly, keeping whatever it
// fetched before the limit.
func (m *MarketData) BackfillStockHistory(ctx context.Context) (*StockHistoryBackfillResult, error) {
	res := &StockHistoryBackfillResult{}
	if m == nil || m.History == nil {
		return res, nil
	}
	rows, err := m.allListings(ctx, domain.SecurityStock)
	if err != nil {
		return res, err
	}
	today := m.today()
	for _, r := range rows {
		if ctx.Err() != nil {
			return res, nil
		}
		sym := r.Security.Ticker
		if existing, herr := m.Store.GetListingDailyHistory(ctx, r.Listing.ID, today, today); herr == nil && len(existing) > 0 {
			res.Skipped++
			continue
		}
		bars, err := m.History.DailyHistory(ctx, sym)
		if err != nil {
			if errors.Is(err, ErrMarketDataThrottled) {
				res.UpstreamThrottled = true
				m.Log.Warn("stock-history backfill hit upstream quota", "symbol", sym)
				return res, nil
			}
			res.UpstreamErrors++
			m.Log.Warn("stock-history backfill failed", "symbol", sym, "err", err.Error())
			m.sleep(ctx)
			continue
		}
		prev := ""
		wrote := 0
		for _, b := range bars {
			bid, ask, serr := stockSpread(b.Close, m.StockSpread)
			if serr != nil {
				continue
			}
			change, cerr := changeAmt(prev, b.Close)
			if cerr != nil {
				change = "0"
			}
			y, mo, d := b.Date.Date()
			day := time.Date(y, mo, d, 0, 0, 0, 0, today.Location())
			if perr := m.Store.UpsertListingDaily(ctx, &domain.ListingDailyPrice{
				ListingID: r.Listing.ID,
				Date:      day,
				Price:     b.Close,
				Ask:       ask,
				Bid:       bid,
				ChangeAmt: change,
				Volume:    b.Volume,
			}); perr != nil {
				res.UpstreamErrors++
				m.Log.Warn("stock-history persist failed", "symbol", sym, "err", perr.Error())
				continue
			}
			m.mirrorToInflux(ctx, r.Listing.ID, day, b.Close, ask, bid, change, b.Volume)
			prev = b.Close
			wrote++
		}
		if wrote > 0 {
			res.SymbolsBackfilled++
			res.RowsWritten += wrote
		}
		m.sleep(ctx)
	}
	return res, nil
}

func (m *MarketData) refreshStocks(ctx context.Context, res *MarketDataResult) error {
	if m.Stocks == nil {
		return nil
	}
	rows, err := m.allListings(ctx, domain.SecurityStock)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return nil
		}
		sym := r.Security.Ticker
		price, change, vol, err := m.Stocks.Quote(ctx, sym)
		if err != nil {
			if errors.Is(err, ErrMarketDataThrottled) {
				res.UpstreamThrottled = true
				m.Log.Warn("market-data refresh hit upstream quota", "symbol", sym)
				return nil
			}
			res.UpstreamErrors++
			m.Log.Warn("market-data stock refresh failed", "symbol", sym, "err", err.Error())
			m.sleep(ctx)
			continue
		}
		bid, ask, err := stockSpread(price, m.StockSpread)
		if err != nil {
			res.UpstreamErrors++
			m.Log.Warn("market-data spread compute failed", "symbol", sym, "err", err.Error())
			continue
		}
		if err := m.persist(ctx, r.Listing, price, ask, bid, change, vol); err != nil {
			res.UpstreamErrors++
			m.Log.Warn("market-data persist failed", "symbol", sym, "err", err.Error())
			continue
		}
		res.StocksUpdated++
		m.sleep(ctx)
	}
	return nil
}

func (m *MarketData) refreshForex(ctx context.Context, res *MarketDataResult) error {
	if m.Forex == nil {
		return nil
	}
	rows, err := m.allListings(ctx, domain.SecurityForex)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return nil
		}
		base, quote := string(r.Security.BaseCurrency), string(r.Security.QuoteCurrency)
		if base == "" || quote == "" {
			res.Skipped++
			continue
		}
		bid, ask, err := m.Forex.FXQuote(ctx, base, quote)
		if err != nil {
			if errors.Is(err, ErrMarketDataThrottled) {
				res.UpstreamThrottled = true
				m.Log.Warn("market-data refresh hit upstream quota", "pair", base+quote)
				return nil
			}
			res.UpstreamErrors++
			m.Log.Warn("market-data forex refresh failed", "pair", base+quote, "err", err.Error())
			m.sleep(ctx)
			continue
		}
		mid, err := midpoint(bid, ask)
		if err != nil {
			res.UpstreamErrors++
			m.Log.Warn("market-data forex mid compute failed", "pair", base+quote, "err", err.Error())
			continue
		}
		change, err := changeAmt(r.Listing.Price, mid)
		if err != nil {
			change = "0"
		}
		if err := m.persist(ctx, r.Listing, mid, ask, bid, change, r.Listing.Volume); err != nil {
			res.UpstreamErrors++
			m.Log.Warn("market-data persist failed", "pair", base+quote, "err", err.Error())
			continue
		}
		res.ForexUpdated++
		m.sleep(ctx)
	}
	return nil
}

// allListings reads every listing of the given security type by paging
// through the catalog. Production has at most a few dozen listings per
// type — paging is more about future-proofing than scale.
func (m *MarketData) allListings(ctx context.Context, t domain.SecurityType) ([]*store.ListListingsRow, error) {
	var out []*store.ListListingsRow
	for page := 1; ; page++ {
		rows, total, err := m.Store.ListListings(ctx, store.ListingFilter{Type: t}, page, 200)
		if err != nil {
			return nil, fmt.Errorf("list %s listings: %w", t, err)
		}
		out = append(out, rows...)
		if int64(len(out)) >= total || len(rows) == 0 {
			break
		}
	}
	return out, nil
}

func (m *MarketData) persist(ctx context.Context, l *domain.Listing, price, ask, bid, change string, volume int64) error {
	updated := *l
	updated.Price = price
	updated.Ask = ask
	updated.Bid = bid
	updated.ChangeAmt = change
	updated.Volume = volume
	if _, err := m.Store.UpsertListing(ctx, &updated); err != nil {
		return fmt.Errorf("upsert listing: %w", err)
	}
	day := m.today()
	if err := m.Store.UpsertListingDaily(ctx, &domain.ListingDailyPrice{
		ListingID: l.ID,
		Date:      day,
		Price:     price,
		Ask:       ask,
		Bid:       bid,
		ChangeAmt: change,
		Volume:    volume,
	}); err != nil {
		return fmt.Errorf("upsert listing daily: %w", err)
	}
	m.mirrorToInflux(ctx, l.ID, day, price, ask, bid, change, volume)
	return nil
}

// mirrorToInflux is a best-effort side-channel write to the Influx
// store (BonusInfluxDB / PR #285). Errors are logged at warn and
// swallowed; Postgres is the canonical store.
func (m *MarketData) mirrorToInflux(ctx context.Context, listingID string, day time.Time, price, ask, bid, change string, volume int64) {
	if m.Influx == nil || !m.Influx.Enabled() {
		return
	}
	if err := m.Influx.WriteDaily(ctx, listingID, day, price, ask, bid, change, volume); err != nil {
		m.Log.Warn("influx mirror write failed", "listing_id", listingID, "err", err.Error())
	}
}

func (m *MarketData) today() time.Time {
	loc := m.Belgrade
	if loc == nil {
		var err error
		loc, err = time.LoadLocation("Europe/Belgrade")
		if err != nil {
			loc = time.UTC
		}
	}
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	y, mo, d := now.In(loc).Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, loc)
}

func (m *MarketData) sleep(ctx context.Context) {
	d := m.Pause
	if d <= 0 {
		// Default below the 5/min free-tier rate. 13s = ~4.6/min, leaves
		// a safety margin without stretching a daily refresh past its
		// quota window.
		d = 13 * time.Second
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// stockSpread returns price·(1-spread), price·(1+spread) as 4-decimal
// strings. Spread values are O(1e-3) constants — float64 → big.Rat is
// safe at that precision.
func stockSpread(price string, spread float64) (bid, ask string, err error) {
	p, err := money.Parse(price)
	if err != nil {
		return "", "", err
	}
	if spread < 0 {
		spread = 0
	}
	sRat := new(big.Rat).SetFloat64(spread)
	if sRat == nil {
		sRat = new(big.Rat)
	}
	one := big.NewRat(1, 1)
	bidRat := money.Mul(p, money.Sub(one, sRat))
	askRat := money.Mul(p, money.Add(one, sRat))
	return money.FormatAmount(bidRat), money.FormatAmount(askRat), nil
}

func midpoint(bid, ask string) (string, error) {
	b, err := money.Parse(bid)
	if err != nil {
		return "", err
	}
	a, err := money.Parse(ask)
	if err != nil {
		return "", err
	}
	half, _ := money.Div(money.Add(a, b), big.NewRat(2, 1))
	return money.FormatRate(half), nil
}

func changeAmt(prev, current string) (string, error) {
	if prev == "" {
		return "0", nil
	}
	p, err := money.Parse(prev)
	if err != nil {
		return "", err
	}
	c, err := money.Parse(current)
	if err != nil {
		return "", err
	}
	return money.FormatAmount(money.Sub(c, p)), nil
}
