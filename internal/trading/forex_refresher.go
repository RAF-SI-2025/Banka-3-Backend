package trading

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/internal/trading/pricing"
	"gorm.io/gorm"
)

// Forex refresher (#230). Pulls latest FX rates from exchangerate-api and
// writes `forex_pairs.exchange_rate`. Spec p.42 lists 8 supported currencies
// → 8x7=56 pairs; one call per base currency covers all quote pairs that
// share it, so a tick costs at most 8 requests.
//
// Cadence matches the price refresher so dashboards see FX moves with the
// same latency as stock prices.
const (
	forexRefreshDefaultInterval = 5 * time.Minute
	forexRefreshPerCallDelay    = 1 * time.Second // open endpoint is generous; small delay smooths bursts.
)

// ForexRatesClient is the narrow interface the forex refresher consumes. Kept
// off pricing.Client because the rates endpoint returns a map, not a Quote.
type ForexRatesClient interface {
	GetRates(ctx context.Context, base string) (map[string]float64, error)
}

type ForexRefresher struct {
	DB           *gorm.DB
	Client       ForexRatesClient
	Interval     time.Duration
	PerCallDelay time.Duration
	Now          func() time.Time
}

func NewForexRefresher(db *gorm.DB, client ForexRatesClient) *ForexRefresher {
	return &ForexRefresher{
		DB:           db,
		Client:       client,
		Interval:     forexRefreshDefaultInterval,
		PerCallDelay: forexRefreshPerCallDelay,
		Now:          time.Now,
	}
}

func (r *ForexRefresher) Start() func() {
	if r.Client == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go r.run(ctx)
	return cancel
}

func (r *ForexRefresher) run(ctx context.Context) {
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

func (r *ForexRefresher) runOnce(ctx context.Context) {
	bases, err := r.loadBases()
	if err != nil {
		log.Printf("[ForexRefresher] load bases: %v", err)
		return
	}
	for i, base := range bases {
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
		if err := r.refreshBase(ctx, base); err != nil {
			if errors.Is(err, pricing.ErrNotFound) {
				continue
			}
			if errors.Is(err, pricing.ErrRateLimited) {
				log.Printf("[ForexRefresher] rate limited; aborting tick after %s", base)
				return
			}
			log.Printf("[ForexRefresher] %s: %v", base, err)
		}
	}
}

// loadBases collects the distinct base currencies in our 56-pair grid. One
// upstream call covers every quote currency that shares the base.
func (r *ForexRefresher) loadBases() ([]string, error) {
	var bases []string
	err := r.DB.Table("forex_pairs").
		Distinct("base_currency").
		Order("base_currency").
		Pluck("base_currency", &bases).Error
	return bases, err
}

func (r *ForexRefresher) refreshBase(ctx context.Context, base string) error {
	rates, err := r.Client.GetRates(ctx, base)
	if err != nil {
		return err
	}

	// One UPDATE per pair under this base. Could be condensed into a CASE
	// WHEN, but the per-base set is at most 7 rows and the simple loop keeps
	// the query inspectable in logs.
	return r.DB.Transaction(func(tx *gorm.DB) error {
		var pairs []ForexPair
		if err := tx.Where("base_currency = ?", base).Find(&pairs).Error; err != nil {
			return err
		}
		for _, p := range pairs {
			rate, ok := rates[p.QuoteCurrency]
			if !ok || rate <= 0 {
				// Quote currency not offered by the upstream — skip rather
				// than overwrite the seed with zero.
				continue
			}
			if err := tx.Model(&ForexPair{}).
				Where("id = ?", p.ID).
				Update("exchange_rate", rate).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
