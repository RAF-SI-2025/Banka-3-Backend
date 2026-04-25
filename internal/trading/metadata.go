package trading

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/internal/trading/pricing"
	"gorm.io/gorm"
)

// Stock metadata syncer (#229). Sibling to the price Refresher and the daily
// Backfiller, but operates at a much lower cadence: spec p.40 lists Alpha
// Vantage's OVERVIEW endpoint as the source of `outstanding_shares` and
// `dividend_yield`, both of which only change quarterly. Weekly is plenty
// and keeps us well clear of AV's free-tier 25-call/day cap.
//
// Why not roll this into the Refresher: different cadence (weekly vs
// 5-min), different table (`stocks` vs `listings`), and OVERVIEW is an
// AV-only endpoint while the Refresher composes Alpaca+AV via MultiClient.
// Keeping them separate lets each loop's rate-limit budget stay simple.
const (
	metadataDefaultInterval = 7 * 24 * time.Hour
	metadataPerCallDelay    = 13 * time.Second // ~4.6 calls/min — under AV's 5/min cap
)

// CompanyOverviewClient is the narrow interface the syncer consumes. Kept
// off pricing.Client because OVERVIEW is AV-only — Alpaca has no equivalent
// endpoint, so a unified interface would just be dead code on that side.
type CompanyOverviewClient interface {
	GetCompanyOverview(ctx context.Context, ticker string) (pricing.CompanyOverview, error)
}

type MetadataSyncer struct {
	DB           *gorm.DB
	Client       CompanyOverviewClient
	Interval     time.Duration
	PerCallDelay time.Duration
	Now          func() time.Time
}

func NewMetadataSyncer(db *gorm.DB, client CompanyOverviewClient) *MetadataSyncer {
	return &MetadataSyncer{
		DB:           db,
		Client:       client,
		Interval:     metadataDefaultInterval,
		PerCallDelay: metadataPerCallDelay,
		Now:          time.Now,
	}
}

// Start mirrors Refresher.Start / Backfiller.Start: nil client => no-op
// cancel so dev/CI without ALPHAVANTAGE_KEY don't spawn an idle goroutine.
func (m *MetadataSyncer) Start() func() {
	if m.Client == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go m.run(ctx)
	return cancel
}

func (m *MetadataSyncer) run(ctx context.Context) {
	// First sync fires immediately so a fresh deploy doesn't sit on
	// week-old seed values until the next tick.
	m.runOnce(ctx)
	t := time.NewTicker(m.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.runOnce(ctx)
		}
	}
}

// metadataTarget is the projection the sync needs. We update the `stocks`
// row directly, so the stock id (not a listing id) is the key.
type metadataTarget struct {
	StockID int64
	Ticker  string
}

func (m *MetadataSyncer) runOnce(ctx context.Context) {
	targets, err := m.loadTargets()
	if err != nil {
		log.Printf("[MetadataSyncer] load targets: %v", err)
		return
	}
	for i, tgt := range targets {
		if ctx.Err() != nil {
			return
		}
		if i > 0 && m.PerCallDelay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.PerCallDelay):
			}
		}
		if err := m.syncOne(ctx, tgt); err != nil {
			if errors.Is(err, pricing.ErrNotFound) {
				continue
			}
			if errors.Is(err, pricing.ErrRateLimited) {
				log.Printf("[MetadataSyncer] rate limited; aborting tick after %s", tgt.Ticker)
				return
			}
			log.Printf("[MetadataSyncer] %s: %v", tgt.Ticker, err)
		}
	}
}

func (m *MetadataSyncer) loadTargets() ([]metadataTarget, error) {
	var rows []metadataTarget
	err := m.DB.Table("stocks").
		Select("id AS stock_id, ticker AS ticker").
		Order("id").
		Scan(&rows).Error
	return rows, err
}

func (m *MetadataSyncer) syncOne(ctx context.Context, tgt metadataTarget) error {
	o, err := m.Client.GetCompanyOverview(ctx, tgt.Ticker)
	if err != nil {
		return err
	}
	return m.DB.Model(&Stock{}).
		Where("id = ?", tgt.StockID).
		Updates(map[string]any{
			"outstanding_shares": o.SharesOutstanding,
			"dividend_yield":     o.DividendYield,
		}).Error
}
