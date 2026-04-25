package trading

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/internal/trading/pricing"
	"gorm.io/gorm"
)

// Options refresher (#230). Pulls per-stock option chains from Yahoo and
// writes premium + implied_volatility back to matching `options` rows. Spec
// p.44 names this endpoint and lists premium/IV as the fields fed by it.
//
// Why a separate refresher (instead of extending price Refresher):
//
//  1. Different provider (Yahoo, no API key) and different shape (per-stock
//     chain, not per-ticker quote).
//  2. Different write target (`options` rows, keyed on stock_id +
//     option_type + strike + settlement_date — see #197 seed format which
//     differs from Yahoo's contract symbol).
//  3. Different cadence — one chain fetch covers many contracts so the
//     bottleneck is unique stock count, not option count. Hourly is plenty
//     and keeps us well clear of Yahoo's soft 429 threshold.
const (
	optionsRefreshDefaultInterval = time.Hour
	optionsRefreshPerCallDelay    = 2 * time.Second // Yahoo has no published cap; spread calls so a many-stock seed doesn't burst.
)

// OptionsChainClient is the narrow interface the options refresher consumes.
// Kept off pricing.Client because chains aren't quote-shaped — adding it to
// the unified interface would just be dead weight on Alpaca/AV.
type OptionsChainClient interface {
	GetOptionsChain(ctx context.Context, ticker string) ([]pricing.OptionContract, error)
}

type OptionsRefresher struct {
	DB           *gorm.DB
	Client       OptionsChainClient
	Interval     time.Duration
	PerCallDelay time.Duration
	Now          func() time.Time
}

func NewOptionsRefresher(db *gorm.DB, client OptionsChainClient) *OptionsRefresher {
	return &OptionsRefresher{
		DB:           db,
		Client:       client,
		Interval:     optionsRefreshDefaultInterval,
		PerCallDelay: optionsRefreshPerCallDelay,
		Now:          time.Now,
	}
}

func (r *OptionsRefresher) Start() func() {
	if r.Client == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go r.run(ctx)
	return cancel
}

func (r *OptionsRefresher) run(ctx context.Context) {
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

// optionsRefreshTarget is the underlying stock we walk per tick. We fetch one
// chain per stock and apply matches to every option row that shares stock_id.
type optionsRefreshTarget struct {
	StockID int64
	Ticker  string
}

func (r *OptionsRefresher) runOnce(ctx context.Context) {
	targets, err := r.loadTargets()
	if err != nil {
		log.Printf("[OptionsRefresher] load targets: %v", err)
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
			if errors.Is(err, pricing.ErrNotFound) {
				continue
			}
			if errors.Is(err, pricing.ErrRateLimited) {
				log.Printf("[OptionsRefresher] rate limited; aborting tick after %s", tgt.Ticker)
				return
			}
			log.Printf("[OptionsRefresher] %s: %v", tgt.Ticker, err)
		}
	}
}

// loadTargets walks the distinct stocks that have at least one option row.
// Stocks with no options would just waste a Yahoo call; the inner join
// filters them.
func (r *OptionsRefresher) loadTargets() ([]optionsRefreshTarget, error) {
	var rows []optionsRefreshTarget
	err := r.DB.Table("stocks AS s").
		Select("s.id AS stock_id, s.ticker AS ticker").
		Joins("JOIN options o ON o.stock_id = s.id").
		Group("s.id, s.ticker").
		Order("s.id").
		Scan(&rows).Error
	return rows, err
}

func (r *OptionsRefresher) refreshOne(ctx context.Context, tgt optionsRefreshTarget) error {
	chain, err := r.Client.GetOptionsChain(ctx, tgt.Ticker)
	if err != nil {
		return err
	}
	// Index the provider chain by (side, strike_cents, settle_date_utc) — the
	// canonical fields. We can't match on contract symbol because the seed
	// format (#197) doesn't follow Yahoo's MSFT220404C00180000 layout.
	type key struct {
		side   string
		strike int64
		settle time.Time
	}
	idx := make(map[key]pricing.OptionContract, len(chain))
	for _, c := range chain {
		idx[key{c.OptionType, c.StrikeCents, c.SettlementDate.UTC().Truncate(24 * time.Hour)}] = c
	}

	var rows []Option
	if err := r.DB.Where("stock_id = ?", tgt.StockID).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	return r.DB.Transaction(func(tx *gorm.DB) error {
		for _, opt := range rows {
			k := key{string(opt.OptionType), opt.StrikePrice, opt.SettlementDate.UTC().Truncate(24 * time.Hour)}
			match, ok := idx[k]
			if !ok {
				continue
			}
			err := tx.Model(&Option{}).
				Where("id = ?", opt.ID).
				Updates(map[string]any{
					"premium":            match.PremiumCents,
					"implied_volatility": match.ImpliedVolatility,
				}).Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}
