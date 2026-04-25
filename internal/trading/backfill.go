package trading

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/internal/trading/pricing"
	"gorm.io/gorm"
)

// Daily-history backfiller (#228). Sibling to the price Refresher in
// refresher.go, but writes into `listing_daily_price_info` instead of
// `listings`. Spec p.40 names Alpha Vantage's TIME_SERIES_DAILY as the
// historical-price source, so this is hard-tied to AV (Alpaca's bars endpoint
// has a different shape and is not in scope).
//
// Two boundaries with the executor we have to respect:
//
//  1. The executor writes `volume` into the same row on every fill via
//     `upsertDailyVolume` (executor.go), and the executor's `nextDelay`
//     formula keys off that volume. Our upsert therefore only updates
//     price/ask/bid/change — never volume — so historical fills aren't
//     clobbered.
//
//  2. AV daily data has no level-1 spread (close-of-day only), so ask/bid
//     mirror price. Same compromise the GLOBAL_QUOTE refresher already
//     makes; the intraday refresher overwrites today's row with a real
//     spread on the next tick anyway.
const (
	backfillDefaultInterval = 24 * time.Hour
	backfillPerCallDelay    = 13 * time.Second // ~4.6 calls/min — under AV's 5/min cap
	backfillWindowDays      = 30               // bound on first run so 25/day cap survives
)

// DailyHistoryClient is the narrow interface the backfiller consumes. The AV
// client satisfies it; tests stub it. Kept off the generic pricing.Client
// interface because Alpaca's bars endpoint isn't part of the issue scope and
// adding a no-op there would just be dead code.
type DailyHistoryClient interface {
	GetDailyHistory(ctx context.Context, ticker string) ([]pricing.DailyBar, error)
}

type Backfiller struct {
	DB           *gorm.DB
	Client       DailyHistoryClient
	Interval     time.Duration
	PerCallDelay time.Duration
	WindowDays   int
	Now          func() time.Time
}

func NewBackfiller(db *gorm.DB, client DailyHistoryClient) *Backfiller {
	return &Backfiller{
		DB:           db,
		Client:       client,
		Interval:     backfillDefaultInterval,
		PerCallDelay: backfillPerCallDelay,
		WindowDays:   backfillWindowDays,
		Now:          time.Now,
	}
}

// Start mirrors Refresher.Start. Nil client => no-op cancel so dev/CI without
// AV keys don't spawn a goroutine that does nothing.
func (b *Backfiller) Start() func() {
	if b.Client == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go b.run(ctx)
	return cancel
}

func (b *Backfiller) run(ctx context.Context) {
	// First pass fires immediately so an operator restart inside market hours
	// gets recent history within the per-call-delay budget rather than after
	// a full day.
	b.runOnce(ctx)
	t := time.NewTicker(b.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.runOnce(ctx)
		}
	}
}

func (b *Backfiller) runOnce(ctx context.Context) {
	targets, err := b.loadTargets()
	if err != nil {
		log.Printf("[Backfiller] load targets: %v", err)
		return
	}
	for i, tgt := range targets {
		if ctx.Err() != nil {
			return
		}
		if i > 0 && b.PerCallDelay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(b.PerCallDelay):
			}
		}
		if err := b.backfillOne(ctx, tgt); err != nil {
			if errors.Is(err, pricing.ErrNotFound) {
				continue
			}
			if errors.Is(err, pricing.ErrRateLimited) {
				log.Printf("[Backfiller] rate limited; aborting tick after %s", tgt.Ticker)
				return
			}
			log.Printf("[Backfiller] %s: %v", tgt.Ticker, err)
		}
	}
}

// loadTargets reuses the same shape as the refresher — same selection rule
// (stock listings only) since AV's TIME_SERIES_DAILY only handles equities.
func (b *Backfiller) loadTargets() ([]refreshTarget, error) {
	var rows []refreshTarget
	err := b.DB.Table("listings AS l").
		Select("l.id AS listing_id, s.ticker AS ticker").
		Joins("JOIN stocks s ON s.id = l.stock_id").
		Where("l.stock_id IS NOT NULL").
		Order("l.id").
		Scan(&rows).Error
	return rows, err
}

func (b *Backfiller) backfillOne(ctx context.Context, tgt refreshTarget) error {
	bars, err := b.Client.GetDailyHistory(ctx, tgt.Ticker)
	if err != nil {
		return err
	}
	if len(bars) == 0 {
		return nil
	}
	// Bars come back newest-first; trim to the configured window. Older bars
	// are dropped on purpose — first-run backfill on the full 100-day compact
	// window would still fit the 25-call cap, but we don't need that depth
	// for the chart history endpoint.
	window := b.WindowDays
	if window <= 0 {
		window = backfillWindowDays
	}
	if len(bars) > window {
		bars = bars[:window]
	}

	return b.DB.Transaction(func(tx *gorm.DB) error {
		for _, bar := range bars {
			// Intraday change (close - open). AV's daily endpoint doesn't
			// hand back a "change" field, and computing close-vs-previous-
			// close would require ordering across rows that aren't all in
			// scope on every run.
			change := bar.CloseCents - bar.OpenCents
			err := tx.Exec(
				`INSERT INTO listing_daily_price_info (listing_id, date, price, ask_price, bid_price, change, volume)
				 VALUES (?, ?, ?, ?, ?, ?, 0)
				 ON CONFLICT (listing_id, date)
				 DO UPDATE SET price = EXCLUDED.price,
				               ask_price = EXCLUDED.ask_price,
				               bid_price = EXCLUDED.bid_price,
				               change = EXCLUDED.change`,
				tgt.ListingID, bar.Date, bar.CloseCents, bar.CloseCents, bar.CloseCents, change,
			).Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}
