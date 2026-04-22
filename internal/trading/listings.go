package trading

import (
	"context"
	"sort"
	"strings"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// listingRow is the denormalized listing+instrument row we load in one
// query. Pointers distinguish "stock listing" vs "future listing" —
// exactly one of the two instrument blocks is populated per row.
type listingRow struct {
	Listing
	ExchangeAcronym   string
	StockTicker       *string
	StockName         *string
	OutstandingShares *int64
	FutureTicker      *string
	FutureName        *string
	ContractSize      *int64
	SettlementDate    *time.Time
}

// fetchListingRows runs the joined query for one or all listings, applying
// the SQL-expressible filters. Derived-field filters/sort are applied in Go
// by callers since they depend on per-instrument formulas.
func (s *Server) fetchListingRows(req *tradingpb.ListListingsRequest, singleID int64) ([]listingRow, error) {
	q := s.db.Table("listings AS l").
		Select(`l.*,
			e.acronym AS exchange_acronym,
			s.ticker AS stock_ticker,
			s.name AS stock_name,
			s.outstanding_shares,
			f.ticker AS future_ticker,
			f.name AS future_name,
			f.contract_size,
			f.settlement_date`).
		Joins("JOIN exchanges e ON e.id = l.exchange_id").
		Joins("LEFT JOIN stocks s ON s.id = l.stock_id").
		Joins("LEFT JOIN futures f ON f.id = l.future_id")

	if singleID != 0 {
		q = q.Where("l.id = ?", singleID)
	}

	if req != nil {
		if p := strings.TrimSpace(req.ExchangePrefix); p != "" {
			q = q.Where("e.acronym ILIKE ?", p+"%")
		}
		if search := strings.TrimSpace(req.Search); search != "" {
			pat := "%" + search + "%"
			q = q.Where(`(s.ticker ILIKE ? OR s.name ILIKE ? OR f.ticker ILIKE ? OR f.name ILIKE ?)`,
				pat, pat, pat, pat)
		}
		q = applyRange(q, "l.price", req.PriceMin, req.PriceMax)
		q = applyRange(q, "l.ask_price", req.AskMin, req.AskMax)
		q = applyRange(q, "l.bid_price", req.BidMin, req.BidMax)
		if req.SettlementFromUnix != 0 || req.SettlementToUnix != 0 {
			// Settlement is only defined for futures — passing a bound
			// implicitly drops stock listings.
			q = q.Where("l.future_id IS NOT NULL")
			if req.SettlementFromUnix != 0 {
				q = q.Where("f.settlement_date >= ?", time.Unix(req.SettlementFromUnix, 0).UTC())
			}
			if req.SettlementToUnix != 0 {
				q = q.Where("f.settlement_date <= ?", time.Unix(req.SettlementToUnix, 0).UTC())
			}
		}
	}

	var rows []listingRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func applyRange(q *gorm.DB, col string, min, max int64) *gorm.DB {
	if min > 0 {
		q = q.Where(col+" >= ?", min)
	}
	if max > 0 {
		q = q.Where(col+" <= ?", max)
	}
	return q
}

// latestDailyInfo returns the most recent listing_daily_price_info row per
// listing id, which is where volume and change live (spec p.46). Missing
// rows are fine — callers default to 0.
func (s *Server) latestDailyInfo(ids []int64) (map[int64]ListingDailyPriceInfo, error) {
	out := map[int64]ListingDailyPriceInfo{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []ListingDailyPriceInfo
	// DISTINCT ON is Postgres-specific; the project is PG-only.
	err := s.db.Raw(`
		SELECT DISTINCT ON (listing_id) *
		FROM listing_daily_price_info
		WHERE listing_id IN (?)
		ORDER BY listing_id, date DESC
	`, ids).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ListingID] = r
	}
	return out, nil
}

// buildListingProto applies the derived-field formulas from spec p.47–48.
// Prices are int64 minor units; derived amounts stay in the same units so
// the formulas become plain integer math (we lose sub-unit precision on the
// 10%/50% splits but that matches the current money model).
func buildListingProto(r listingRow, today ListingDailyPriceInfo) *tradingpb.Listing {
	out := &tradingpb.Listing{
		Id:              r.ID,
		ExchangeId:      r.ExchangeID,
		Price:           r.Price,
		AskPrice:        r.AskPrice,
		BidPrice:        r.BidPrice,
		LastRefreshUnix: r.LastRefresh.Unix(),
		Volume:          today.Volume,
		Change:          today.Change,
		ExchangeAcronym: r.ExchangeAcronym,
	}
	if r.StockID != nil {
		out.StockId = *r.StockID
		out.SecurityType = "stock"
		if r.StockTicker != nil {
			out.Ticker = *r.StockTicker
		}
		if r.StockName != nil {
			out.Name = *r.StockName
		}
		out.ContractSize = 1
		out.MaintenanceMargin = r.Price / 2
		if r.OutstandingShares != nil {
			out.MarketCap = *r.OutstandingShares * r.Price
		}
	}
	if r.FutureID != nil {
		out.FutureId = *r.FutureID
		out.SecurityType = "future"
		if r.FutureTicker != nil {
			out.Ticker = *r.FutureTicker
		}
		if r.FutureName != nil {
			out.Name = *r.FutureName
		}
		if r.ContractSize != nil {
			out.ContractSize = *r.ContractSize
			out.MaintenanceMargin = (*r.ContractSize * r.Price) / 10
			out.NominalValue = *r.ContractSize * r.Price
		}
		if r.SettlementDate != nil {
			out.SettlementDateUnix = r.SettlementDate.Unix()
		}
	}
	out.InitialMarginCost = (out.MaintenanceMargin * 11) / 10
	return out
}

// ListListings replaces the older placeholder implementation. Filters are
// expressed on the request; derived-field filters (volume) and derived-field
// sort (maintenance_margin) run in Go after rows are assembled.
func (s *Server) ListListings(_ context.Context, req *tradingpb.ListListingsRequest) (*tradingpb.ListListingsResponse, error) {
	rows, err := s.fetchListingRows(req, 0)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	daily, err := s.latestDailyInfo(ids)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	out := make([]*tradingpb.Listing, 0, len(rows))
	for _, r := range rows {
		out = append(out, buildListingProto(r, daily[r.ID]))
	}

	// Volume filter runs post-derive since volume is not stored on the
	// listings row.
	if req != nil && (req.VolumeMin > 0 || req.VolumeMax > 0) {
		filtered := out[:0]
		for _, l := range out {
			if req.VolumeMin > 0 && l.Volume < req.VolumeMin {
				continue
			}
			if req.VolumeMax > 0 && l.Volume > req.VolumeMax {
				continue
			}
			filtered = append(filtered, l)
		}
		out = filtered
	}

	if req != nil && req.SortBy != "" {
		if err := sortListings(out, req.SortBy, req.SortOrder); err != nil {
			return nil, err
		}
	}

	return &tradingpb.ListListingsResponse{Listings: out}, nil
}

func sortListings(rows []*tradingpb.Listing, sortBy, sortOrder string) error {
	var less func(a, b *tradingpb.Listing) bool
	switch sortBy {
	case "price":
		less = func(a, b *tradingpb.Listing) bool { return a.Price < b.Price }
	case "volume":
		less = func(a, b *tradingpb.Listing) bool { return a.Volume < b.Volume }
	case "maintenance_margin":
		less = func(a, b *tradingpb.Listing) bool { return a.MaintenanceMargin < b.MaintenanceMargin }
	default:
		return status.Errorf(codes.InvalidArgument, "invalid sort_by %q", sortBy)
	}
	desc := sortOrder == "desc"
	sort.SliceStable(rows, func(i, j int) bool {
		if desc {
			return less(rows[j], rows[i])
		}
		return less(rows[i], rows[j])
	})
	return nil
}

// GetListing returns a single row enriched with the same derived fields as
// ListListings. 404 when the row is missing.
func (s *Server) GetListing(_ context.Context, req *tradingpb.GetListingRequest) (*tradingpb.GetListingResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	rows, err := s.fetchListingRows(nil, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if len(rows) == 0 {
		return nil, status.Error(codes.NotFound, "listing not found")
	}
	daily, err := s.latestDailyInfo([]int64{req.Id})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &tradingpb.GetListingResponse{
		Listing: buildListingProto(rows[0], daily[req.Id]),
	}, nil
}

// ListListingHistory returns the daily price info rows for the requested
// period, ordered ascending by date so the chart can plot directly. "all"
// skips the lower bound entirely.
func (s *Server) ListListingHistory(_ context.Context, req *tradingpb.ListListingHistoryRequest) (*tradingpb.ListListingHistoryResponse, error) {
	if req.ListingId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "listing_id required")
	}
	since, ok := periodStart(req.Period, time.Now())
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "invalid period %q", req.Period)
	}

	// Confirm the listing exists so callers get 404 instead of an empty
	// series when they type the wrong id.
	var count int64
	if err := s.db.Model(&Listing{}).Where("id = ?", req.ListingId).Count(&count).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if count == 0 {
		return nil, status.Error(codes.NotFound, "listing not found")
	}

	q := s.db.Where("listing_id = ?", req.ListingId).Order("date ASC")
	if !since.IsZero() {
		q = q.Where("date >= ?", since)
	}
	var rows []ListingDailyPriceInfo
	if err := q.Find(&rows).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	points := make([]*tradingpb.ListingHistoryPoint, 0, len(rows))
	for _, r := range rows {
		points = append(points, &tradingpb.ListingHistoryPoint{
			DateUnix: r.Date.Unix(),
			Price:    r.Price,
			AskPrice: r.AskPrice,
			BidPrice: r.BidPrice,
			Change:   r.Change,
			Volume:   r.Volume,
		})
	}
	return &tradingpb.ListListingHistoryResponse{Points: points}, nil
}

// periodStart maps the spec's period strings to a lower-bound date. "all"
// returns the zero time, which the caller treats as "no lower bound".
func periodStart(period string, now time.Time) (time.Time, bool) {
	switch period {
	case "day":
		return now.AddDate(0, 0, -1), true
	case "week":
		return now.AddDate(0, 0, -7), true
	case "month":
		return now.AddDate(0, -1, 0), true
	case "year":
		return now.AddDate(-1, 0, 0), true
	case "5y":
		return now.AddDate(-5, 0, 0), true
	case "all", "":
		return time.Time{}, true
	}
	return time.Time{}, false
}

// ListForexPairs surfaces every forex pair. Clients are forbidden per spec
// p.58 — only actuaries (employees) may see forex. caller_email must be set;
// the trading server asks bank which role it maps to.
func (s *Server) ListForexPairs(ctx context.Context, req *tradingpb.ListForexPairsRequest) (*tradingpb.ListForexPairsResponse, error) {
	caller, err := s.bank.ResolveCaller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.IsClient {
		return nil, status.Error(codes.PermissionDenied, "forex is available to employees only")
	}
	_ = req // caller_email is forwarded but bank.ResolveCaller reads it from context metadata

	var rows []ForexPair
	if err := s.db.Find(&rows).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	out := make([]*tradingpb.ForexPair, 0, len(rows))
	for _, r := range rows {
		// Forex derived fields (spec p.48): contract_size = 1000 by default,
		// maintenance_margin = contract_size * price * 10%, nominal_value =
		// contract_size * price. "price" for forex is exchange_rate, and we
		// cast to int64 minor units the same way the rest of the system
		// treats monetary values.
		price := int64(r.ExchangeRate * 100)
		contract := int64(1000)
		out = append(out, &tradingpb.ForexPair{
			Id:                r.ID,
			Ticker:            r.Ticker,
			Name:              r.Name,
			BaseCurrency:      r.BaseCurrency,
			QuoteCurrency:     r.QuoteCurrency,
			ExchangeRate:      r.ExchangeRate,
			Liquidity:         string(r.Liquidity),
			ContractSize:      contract,
			MaintenanceMargin: (contract * price) / 10,
			NominalValue:      contract * price,
		})
	}
	return &tradingpb.ListForexPairsResponse{Pairs: out}, nil
}
