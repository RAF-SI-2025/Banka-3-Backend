package trading

import (
	"errors"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func parseOrderType(s string) (OrderType, error) {
	switch s {
	case "market":
		return OrderMarket, nil
	case "limit":
		return OrderLimit, nil
	case "stop":
		return OrderStop, nil
	case "stop_limit":
		return OrderStopLimit, nil
	}
	return "", status.Errorf(codes.InvalidArgument, "invalid order_type %q", s)
}

func parseDirection(s string) (OrderDirection, error) {
	switch s {
	case "buy":
		return DirectionBuy, nil
	case "sell":
		return DirectionSell, nil
	}
	return "", status.Errorf(codes.InvalidArgument, "invalid direction %q", s)
}

// validatePriceFields enforces that limit/stop prices are provided exactly
// when the order type needs them. Market orders derive price from the book
// at execution time (issue #189).
func validatePriceFields(ot OrderType, limit, stop int64) error {
	switch ot {
	case OrderMarket:
		if limit != 0 || stop != 0 {
			return status.Error(codes.InvalidArgument, "market orders must not set limit_price or stop_price")
		}
	case OrderLimit:
		if limit <= 0 {
			return status.Error(codes.InvalidArgument, "limit orders require limit_price > 0")
		}
		if stop != 0 {
			return status.Error(codes.InvalidArgument, "limit orders must not set stop_price")
		}
	case OrderStop:
		if stop <= 0 {
			return status.Error(codes.InvalidArgument, "stop orders require stop_price > 0")
		}
		if limit != 0 {
			return status.Error(codes.InvalidArgument, "stop orders must not set limit_price")
		}
	case OrderStopLimit:
		if limit <= 0 || stop <= 0 {
			return status.Error(codes.InvalidArgument, "stop_limit orders require both limit_price and stop_price > 0")
		}
	}
	return nil
}

// marketReferencePrice stores the user-provided price on the row so that
// execution later (#189+) has a stable quote to compare against. Market
// orders land with 0 — execution reads from the listing at fill time.
func marketReferencePrice(ot OrderType, req *tradingpb.CreateOrderRequest) int64 {
	switch ot {
	case OrderLimit, OrderStopLimit:
		return req.LimitPrice
	case OrderStop:
		return req.StopPrice
	}
	return 0
}

// instrumentInfo bundles everything CreateOrder needs to know about the
// underlying for auth, accounting, and approval routing. Exchange is nil for
// forex pairs (they have no listing/exchange per spec). SettlementDate is set
// only for futures and options — stocks and forex don't expire. MarketPrice is
// the reference price used when the order type is `market` (issue #200's
// approximate-price formula — page 57).
type instrumentInfo struct {
	Exchange       *Exchange
	Currency       string
	ContractSize   int64
	MarketPrice    int64
	SettlementDate *time.Time
}

// resolveInstrument loads the underlying referenced by the CreateOrder request
// and returns the derived fields used downstream. Listings inherit currency
// from their exchange; options follow the underlying stock's listing; forex
// pairs aren't tied to any exchange and use the quote currency. Callers that
// need the clock (IsOpen / IsAfterHours) should skip forex.
func (s *Server) resolveInstrument(req *tradingpb.CreateOrderRequest) (*instrumentInfo, error) {
	set := 0
	if req.ListingId != 0 {
		set++
	}
	if req.OptionId != 0 {
		set++
	}
	if req.ForexPairId != 0 {
		set++
	}
	if set != 1 {
		return nil, status.Error(codes.InvalidArgument, "exactly one of listing_id, option_id, forex_pair_id must be set")
	}

	switch {
	case req.ListingId != 0:
		var listing Listing
		if err := s.db.First(&listing, req.ListingId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Error(codes.NotFound, "listing not found")
			}
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		var exch Exchange
		if err := s.db.First(&exch, listing.ExchangeID).Error; err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		info := &instrumentInfo{
			Exchange:     &exch,
			Currency:     exch.Currency,
			ContractSize: 1,
			MarketPrice:  listing.Price,
		}
		if listing.FutureID != nil {
			var fut Future
			if err := s.db.First(&fut, *listing.FutureID).Error; err != nil {
				return nil, status.Errorf(codes.Internal, "%v", err)
			}
			info.ContractSize = fut.ContractSize
			sd := fut.SettlementDate
			info.SettlementDate = &sd
		}
		return info, nil

	case req.OptionId != 0:
		var opt Option
		if err := s.db.First(&opt, req.OptionId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Error(codes.NotFound, "option not found")
			}
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		var listing Listing
		if err := s.db.Where("stock_id = ?", opt.StockID).First(&listing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Error(codes.FailedPrecondition, "underlying stock has no listing")
			}
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		var exch Exchange
		if err := s.db.First(&exch, listing.ExchangeID).Error; err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		sd := opt.SettlementDate
		return &instrumentInfo{
			Exchange:       &exch,
			Currency:       exch.Currency,
			ContractSize:   1,
			MarketPrice:    opt.Premium,
			SettlementDate: &sd,
		}, nil

	default: // forex pair — no exchange
		var fx ForexPair
		if err := s.db.First(&fx, req.ForexPairId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Error(codes.NotFound, "forex pair not found")
			}
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		// Forex convention (spec p.48, mirrored by ListForexPairs): contract
		// size 1000, price in minor units is exchange_rate * 100.
		return &instrumentInfo{
			Currency:     fx.QuoteCurrency,
			ContractSize: 1000,
			MarketPrice:  int64(fx.ExchangeRate * 100),
		}, nil
	}
}
