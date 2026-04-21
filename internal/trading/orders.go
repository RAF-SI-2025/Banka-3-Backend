package trading

import (
	"errors"

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

// resolveInstrument returns (exchange, currency) for the order's instrument.
// Listings inherit both from their exchange; options follow the underlying
// stock's listing to its exchange; forex pairs aren't tied to any exchange
// and use the quote currency (exchange is returned as nil). Callers that
// need the clock (IsOpen / IsAfterHours) should skip forex.
func (s *Server) resolveInstrument(req *tradingpb.CreateOrderRequest) (*Exchange, string, error) {
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
		return nil, "", status.Error(codes.InvalidArgument, "exactly one of listing_id, option_id, forex_pair_id must be set")
	}

	switch {
	case req.ListingId != 0:
		var listing Listing
		if err := s.db.First(&listing, req.ListingId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, "", status.Error(codes.NotFound, "listing not found")
			}
			return nil, "", status.Errorf(codes.Internal, "%v", err)
		}
		var exch Exchange
		if err := s.db.First(&exch, listing.ExchangeID).Error; err != nil {
			return nil, "", status.Errorf(codes.Internal, "%v", err)
		}
		return &exch, exch.Currency, nil

	case req.OptionId != 0:
		var opt Option
		if err := s.db.First(&opt, req.OptionId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, "", status.Error(codes.NotFound, "option not found")
			}
			return nil, "", status.Errorf(codes.Internal, "%v", err)
		}
		var listing Listing
		if err := s.db.Where("stock_id = ?", opt.StockID).First(&listing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, "", status.Error(codes.FailedPrecondition, "underlying stock has no listing")
			}
			return nil, "", status.Errorf(codes.Internal, "%v", err)
		}
		var exch Exchange
		if err := s.db.First(&exch, listing.ExchangeID).Error; err != nil {
			return nil, "", status.Errorf(codes.Internal, "%v", err)
		}
		return &exch, exch.Currency, nil

	default: // forex pair — no exchange
		var fx ForexPair
		if err := s.db.First(&fx, req.ForexPairId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, "", status.Error(codes.NotFound, "forex pair not found")
			}
			return nil, "", status.Errorf(codes.Internal, "%v", err)
		}
		return nil, fx.QuoteCurrency, nil
	}
}

// lookupEmployeeID resolves an employee email to its bank-side id. We query
// the employees table directly since we already have a DB handle; going
// through the user gRPC service would add a hop for data that lives here.
func lookupEmployeeID(tx *gorm.DB, email string) (int64, error) {
	var row struct{ ID int64 }
	err := tx.Table("employees").
		Select("id").
		Where("email = ?", email).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, status.Error(codes.NotFound, "employee not found")
		}
		return 0, status.Errorf(codes.Internal, "%v", err)
	}
	return row.ID, nil
}
