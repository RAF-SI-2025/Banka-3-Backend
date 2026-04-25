package trading

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/internal/trading/pricing"
	"gorm.io/gorm"
)

// External-pricing refresher (#184). Walks stock listings on a fixed
// interval, asks the configured pricing client for a fresh quote, and
// writes price/ask/bid back to the row. Implements the spec's "Podaci o
// cenama biće preuzimani iz eksternog izvora" requirement (p.37): listings
// were seeded statically by #195 and stay frozen without something to keep
// them moving.
//
// Two design notes:
//
//  1. Listings without an Alpaca/Alpha-Vantage-shaped ticker (futures,
//     foreign exchanges, dummy tickers) are not refreshable from these
//     providers. We filter on stock_id IS NOT NULL up front and let
//     ErrNotFound from the client skip individual rows, so a single
//     unrecognized ticker never blocks the rest.
//
//  2. Per-quote spacing: AV's free tier caps at 5/min. We sleep between
//     calls inside a tick rather than fanning out goroutines so the rate
//     limit holds without an external limiter library.
//
// Pricing client is optional: nil disables the refresher. Operators wire it
// up only when they've supplied API keys via env (see cmd/bank/main.go).
const (
	refresherDefaultInterval = 5 * time.Minute
	refresherPerCallDelay    = 13 * time.Second // ~4.6 calls/min — under AV's 5/min cap
)

type Refresher struct {
	DB       *gorm.DB
	Client   pricing.Client
	Interval time.Duration
	// PerCallDelay throttles successive calls within one tick. Defaulted to
	// refresherPerCallDelay; tests override it to 0.
	PerCallDelay time.Duration
	// Now is overridden in tests so the last_refresh stamp is deterministic.
	Now func() time.Time
}

func NewRefresher(db *gorm.DB, client pricing.Client) *Refresher {
	return &Refresher{
		DB:           db,
		Client:       client,
		Interval:     refresherDefaultInterval,
		PerCallDelay: refresherPerCallDelay,
		Now:          time.Now,
	}
}

// Start kicks the refresher loop and returns a cancel func mirroring
// StartExecutor's shape so cmd/bank/main.go can stop both with the same
// pattern.
func (r *Refresher) Start() func() {
	if r.Client == nil {
		// Nothing wired up — return a no-op cancel rather than spinning a
		// goroutine that does nothing.
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go r.run(ctx)
	return cancel
}

func (r *Refresher) run(ctx context.Context) {
	// First refresh fires immediately so an operator who restarts during
	// market hours sees prices move within seconds, not minutes.
	r.runOnce(ctx)
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.runOnce(ctx)
		}
	}
}

// refreshTarget is the projection we actually need. Listings table holds
// stock_id but the ticker lives on stocks; one join keeps the per-row
// fetch+update simple.
type refreshTarget struct {
	ListingID int64
	Ticker    string
}

func (r *Refresher) runOnce(ctx context.Context) {
	targets, err := r.loadTargets()
	if err != nil {
		log.Printf("[Refresher] load targets: %v", err)
		return
	}
	for i, tgt := range targets {
		if ctx.Err() != nil {
			return
		}
		if i > 0 && r.PerCallDelay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(r.PerCallDelay):
			}
		}
		if err := r.refreshOne(ctx, tgt); err != nil {
			// ErrNotFound is logged at debug level worth: it's expected for
			// dummy tickers in the seed (e.g. RAFA/RAFB) that don't exist on
			// either provider. We collapse it to a single line so logs stay
			// readable.
			if errors.Is(err, pricing.ErrNotFound) {
				continue
			}
			if errors.Is(err, pricing.ErrRateLimited) {
				log.Printf("[Refresher] rate limited; aborting tick after %s", tgt.Ticker)
				return
			}
			log.Printf("[Refresher] %s: %v", tgt.Ticker, err)
		}
	}
}

func (r *Refresher) loadTargets() ([]refreshTarget, error) {
	var rows []refreshTarget
	err := r.DB.Table("listings AS l").
		Select("l.id AS listing_id, s.ticker AS ticker").
		Joins("JOIN stocks s ON s.id = l.stock_id").
		Where("l.stock_id IS NOT NULL").
		Order("l.id").
		Scan(&rows).Error
	return rows, err
}

func (r *Refresher) refreshOne(ctx context.Context, tgt refreshTarget) error {
	q, err := r.Client.GetQuote(ctx, tgt.Ticker)
	if err != nil {
		return err
	}
	now := r.Now().UTC()
	return r.DB.Model(&Listing{}).
		Where("id = ?", tgt.ListingID).
		Updates(map[string]any{
			"price":        q.PriceCents,
			"ask_price":    q.AskCents,
			"bid_price":    q.BidCents,
			"last_refresh": now,
		}).Error
}
