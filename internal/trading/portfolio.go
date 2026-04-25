package trading

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"github.com/RAF-SI-2025/Banka-3-Backend/internal/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// holdingAssetType maps the holding's polymorphic FK to the short type tag
// the portal renders ("stock", "future", "forex", "option"). Exactly one of
// the FKs is set on every row (DB CHECK), so the order of the cases doesn't
// matter — at most one matches.
func holdingAssetType(h *Holding) string {
	switch {
	case h.StockID != nil:
		return "stock"
	case h.FutureID != nil:
		return "future"
	case h.ForexPairID != nil:
		return "forex"
	case h.OptionID != nil:
		return "option"
	}
	return ""
}

// holdingTickerAndPrice resolves the asset's display ticker and current quote
// in the instrument's native currency. Listings inherit ticker from their
// underlying stock/future row; options carry one directly; forex pairs use
// the BASE/QUOTE ticker. The current price uses the same conventions as
// resolveInstrument (listing.price for stocks/futures, option.premium for
// options, exchange_rate*100 for forex) so portfolio P/L tracks the same
// quote the order placement page shows.
func (s *Server) holdingTickerAndPrice(tx *gorm.DB, h *Holding) (string, int64, string, error) {
	switch {
	case h.StockID != nil:
		var st Stock
		if err := tx.Select("ticker").First(&st, *h.StockID).Error; err != nil {
			return "", 0, "", err
		}
		var l Listing
		if err := tx.Where("stock_id = ?", *h.StockID).First(&l).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return st.Ticker, 0, "", nil
			}
			return "", 0, "", err
		}
		var ex Exchange
		if err := tx.Select("currency").First(&ex, l.ExchangeID).Error; err != nil {
			return "", 0, "", err
		}
		return st.Ticker, l.Price, ex.Currency, nil
	case h.FutureID != nil:
		var ft Future
		if err := tx.Select("ticker").First(&ft, *h.FutureID).Error; err != nil {
			return "", 0, "", err
		}
		var l Listing
		if err := tx.Where("future_id = ?", *h.FutureID).First(&l).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ft.Ticker, 0, "", nil
			}
			return "", 0, "", err
		}
		var ex Exchange
		if err := tx.Select("currency").First(&ex, l.ExchangeID).Error; err != nil {
			return "", 0, "", err
		}
		return ft.Ticker, l.Price, ex.Currency, nil
	case h.OptionID != nil:
		var opt Option
		if err := tx.Select("ticker, stock_id, premium").First(&opt, *h.OptionID).Error; err != nil {
			return "", 0, "", err
		}
		var l Listing
		if err := tx.Where("stock_id = ?", opt.StockID).First(&l).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return opt.Ticker, opt.Premium, "", nil
			}
			return "", 0, "", err
		}
		var ex Exchange
		if err := tx.Select("currency").First(&ex, l.ExchangeID).Error; err != nil {
			return "", 0, "", err
		}
		return opt.Ticker, opt.Premium, ex.Currency, nil
	case h.ForexPairID != nil:
		var fx ForexPair
		if err := tx.First(&fx, *h.ForexPairID).Error; err != nil {
			return "", 0, "", err
		}
		return fx.Ticker, int64(fx.ExchangeRate * 100), fx.QuoteCurrency, nil
	}
	return "", 0, "", status.Error(codes.Internal, "holding has no asset reference")
}

// convertToAccountCcy converts an amount in `from` to the account's currency
// via the bank's RSD-anchored rate table. Same-currency calls short-circuit
// and never hit the DB. Used for portfolio profit display so the ledger lines
// up with what would actually settle on a sell.
func (s *Server) convertToAccountCcy(amount int64, from, accCurrency string) (int64, error) {
	if from == "" || from == accCurrency {
		return amount, nil
	}
	rateAcc, err := s.bank.GetExchangeRateToRSD(accCurrency)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "%v", err)
	}
	rateFrom, err := s.bank.GetExchangeRateToRSD(from)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "%v", err)
	}
	return int64(math.Round(float64(amount) * rateFrom / rateAcc)), nil
}

// holdingToProto fills in the display-only fields (ticker, current_price,
// profit, asset_type) on top of the stored row. Profit is rendered in the
// holding's account currency: avg_cost is already there, current_price gets
// FX-converted before the subtraction. Holdings whose asset has no listing
// surface a 0 price rather than failing — the portal still wants to show the
// position so the user can sell it (or wait for a listing).
func (s *Server) holdingToProto(tx *gorm.DB, h *Holding) (*tradingpb.Holding, error) {
	ticker, price, instrCcy, err := s.holdingTickerAndPrice(tx, h)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	var accNumber, accCcy string
	{
		var row struct {
			Number   string
			Currency string
		}
		if err := tx.Table("accounts").Select("number, currency").Where("id = ?", h.AccountID).Take(&row).Error; err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
		accNumber = row.Number
		accCcy = row.Currency
	}
	priceInAcc, err := s.convertToAccountCcy(price, instrCcy, accCcy)
	if err != nil {
		return nil, err
	}
	profit := (priceInAcc - h.AvgCost) * h.Amount

	out := &tradingpb.Holding{
		Id:               h.ID,
		PlacerId:         h.PlacerID,
		Amount:           h.Amount,
		AvgCost:          h.AvgCost,
		AccountId:        h.AccountID,
		AccountNumber:    accNumber,
		PublicAmount:     h.PublicAmount,
		Ticker:           ticker,
		CurrentPrice:     price,
		Profit:           profit,
		LastModifiedUnix: h.LastModified.Unix(),
		AssetType:        holdingAssetType(h),
	}
	if h.StockID != nil {
		out.StockId = *h.StockID
	}
	if h.FutureID != nil {
		out.FutureId = *h.FutureID
	}
	if h.ForexPairID != nil {
		out.ForexPairId = *h.ForexPairID
	}
	if h.OptionID != nil {
		out.OptionId = *h.OptionID
	}
	return out, nil
}

// ListHoldings returns every holding owned by the caller — clients see their
// own, employees see whatever they've placed orders for. A caller with no
// placer row yet (no orders ever placed) gets an empty list rather than a
// 404; the placer row is created lazily so subsequent holdings calls dedupe.
func (s *Server) ListHoldings(ctx context.Context, _ *tradingpb.ListHoldingsRequest) (*tradingpb.ListHoldingsResponse, error) {
	caller, err := s.bank.ResolveCaller(ctx)
	if err != nil {
		return nil, err
	}

	var out []*tradingpb.Holding
	err = s.db.Transaction(func(tx *gorm.DB) error {
		placerID, err := resolvePlacerForCaller(tx, caller)
		if err != nil {
			return err
		}
		var holdings []Holding
		if err := tx.Where("placer_id = ?", placerID).Order("last_modified DESC").Find(&holdings).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		out = make([]*tradingpb.Holding, 0, len(holdings))
		for i := range holdings {
			p, err := s.holdingToProto(tx, &holdings[i])
			if err != nil {
				return err
			}
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &tradingpb.ListHoldingsResponse{Holdings: out}, nil
}

// SellHolding is the spec's `POST /api/portfolio/sell` thin wrapper. Resolves
// the holding to its underlying asset, verifies ownership, and forwards a
// CreateOrder with direction=sell so the same approval / execution / margin
// pipeline applies. Quantity is clamped at validation time so a user can't
// place a sell larger than what they hold; the executor's per-fill check
// stays as the final safety net.
func (s *Server) SellHolding(ctx context.Context, req *tradingpb.SellHoldingRequest) (*tradingpb.SellHoldingResponse, error) {
	if req.HoldingId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "holding_id required")
	}
	if req.Quantity <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be positive")
	}
	if strings.TrimSpace(req.AccountNumber) == "" {
		return nil, status.Error(codes.InvalidArgument, "account_number required")
	}
	if req.OrderType == "" {
		return nil, status.Error(codes.InvalidArgument, "order_type required")
	}

	caller, err := s.bank.ResolveCaller(ctx)
	if err != nil {
		return nil, err
	}

	var h Holding
	if err := s.db.First(&h, req.HoldingId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "holding not found")
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if err := s.authorizeHoldingOwner(s.db, &h, caller); err != nil {
		return nil, err
	}
	if req.Quantity > h.Amount {
		return nil, status.Error(codes.FailedPrecondition, "quantity exceeds held amount")
	}

	createReq := &tradingpb.CreateOrderRequest{
		AccountNumber: req.AccountNumber,
		OrderType:     req.OrderType,
		Direction:     "sell",
		Quantity:      req.Quantity,
		LimitPrice:    req.LimitPrice,
		StopPrice:     req.StopPrice,
		AllOrNone:     req.AllOrNone,
	}
	switch {
	case h.StockID != nil, h.FutureID != nil:
		listingID, err := listingIDForHolding(s.db, &h)
		if err != nil {
			return nil, err
		}
		createReq.ListingId = listingID
	case h.OptionID != nil:
		createReq.OptionId = *h.OptionID
	case h.ForexPairID != nil:
		createReq.ForexPairId = *h.ForexPairID
	}

	resp, err := s.CreateOrder(ctx, createReq)
	if err != nil {
		return nil, err
	}
	return &tradingpb.SellHoldingResponse{OrderId: resp.OrderId, Status: resp.Status}, nil
}

// listingIDForHolding finds the listing row for a stock or future holding so
// the wrapped CreateOrder call can populate listing_id (orders never reference
// stocks/futures directly — that's the listing's job). Returns FailedPrecondition
// when the underlying has no listing on any exchange.
func listingIDForHolding(db *gorm.DB, h *Holding) (int64, error) {
	var col string
	var val int64
	switch {
	case h.StockID != nil:
		col, val = "stock_id", *h.StockID
	case h.FutureID != nil:
		col, val = "future_id", *h.FutureID
	default:
		return 0, status.Error(codes.Internal, "listingIDForHolding called on non-listing asset")
	}
	var id int64
	err := db.Table("listings").Select("id").Where(col+" = ?", val).Take(&id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, status.Error(codes.FailedPrecondition, "no listing for this asset")
		}
		return 0, status.Errorf(codes.Internal, "%v", err)
	}
	return id, nil
}

// authorizeHoldingOwner enforces "the caller owns this holding". Holdings are
// scoped to the placer row, which is keyed per identity (client or employee)
// — so the check reduces to comparing the caller's identity against the
// placer the holding points at.
func (s *Server) authorizeHoldingOwner(db *gorm.DB, h *Holding, caller *bank.CallerIdentity) error {
	var placer OrderPlacer
	if err := db.First(&placer, h.PlacerID).Error; err != nil {
		return status.Errorf(codes.Internal, "%v", err)
	}
	if caller.IsClient && placer.ClientID != nil && *placer.ClientID == caller.ClientID {
		return nil
	}
	if caller.IsEmployee && placer.EmployeeID != nil {
		var empID int64
		if err := db.Table("employees").Select("id").Where("email = ?", caller.Email).Take(&empID).Error; err == nil && empID == *placer.EmployeeID {
			return nil
		}
	}
	return status.Error(codes.PermissionDenied, "caller does not own this holding")
}

// SetHoldingPublic adjusts the OTC-discoverable share count on a stock
// holding. Validation fans out into three buckets so the frontend can wire
// each to a different inline error: NotFound for a stale id, PermissionDenied
// for a wrong-owner attempt, FailedPrecondition for a non-stock asset (the
// schema CHECK enforces 0 there), and InvalidArgument for an out-of-range
// public_amount (must satisfy 0 ≤ public_amount ≤ amount).
func (s *Server) SetHoldingPublic(ctx context.Context, req *tradingpb.SetHoldingPublicRequest) (*tradingpb.SetHoldingPublicResponse, error) {
	if req.HoldingId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "holding_id required")
	}
	if req.PublicAmount < 0 {
		return nil, status.Error(codes.InvalidArgument, "public_amount must be >= 0")
	}
	caller, err := s.bank.ResolveCaller(ctx)
	if err != nil {
		return nil, err
	}

	var detail *tradingpb.Holding
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var h Holding
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&h, req.HoldingId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.NotFound, "holding not found")
			}
			return status.Errorf(codes.Internal, "%v", err)
		}
		if err := s.authorizeHoldingOwner(tx, &h, caller); err != nil {
			return err
		}
		if h.StockID == nil {
			return status.Error(codes.FailedPrecondition, "public_amount applies to stock holdings only")
		}
		if req.PublicAmount > h.Amount {
			return status.Error(codes.InvalidArgument, "public_amount cannot exceed amount")
		}

		if err := tx.Model(&Holding{}).Where("id = ?", h.ID).Updates(map[string]any{
			"public_amount": req.PublicAmount,
			"last_modified": time.Now(),
		}).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		h.PublicAmount = req.PublicAmount
		h.LastModified = time.Now()

		p, err := s.holdingToProto(tx, &h)
		if err != nil {
			return err
		}
		detail = p
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &tradingpb.SetHoldingPublicResponse{Holding: detail}, nil
}

// ExerciseOption settles a held option against the spot price (spec p.62).
// Actuary-only, the option must not be past its settlement date, and it has
// to be in-the-money. Payout is paid out of the bank-stub system account in
// the option's underlying currency, then converted to the chosen account's
// currency on credit — same FX path the executor uses for cross-currency
// fills. The holding is fully consumed by exercise (all units settle at once
// per spec — partial exercise isn't surfaced).
func (s *Server) ExerciseOption(ctx context.Context, req *tradingpb.ExerciseOptionRequest) (*tradingpb.ExerciseOptionResponse, error) {
	if req.OptionId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "option_id required")
	}
	if strings.TrimSpace(req.AccountNumber) == "" {
		return nil, status.Error(codes.InvalidArgument, "account_number required")
	}
	caller, err := s.bank.ResolveCaller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.IsClient {
		return nil, status.Error(codes.PermissionDenied, "options are available to employees only")
	}

	acc, err := s.bank.GetAccountByNumber(req.AccountNumber)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "account not found")
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if err := s.bank.AuthorizeAccountAccess(ctx, acc); err != nil {
		return nil, err
	}

	var payout, qty int64
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var opt Option
		if err := tx.First(&opt, req.OptionId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.NotFound, "option not found")
			}
			return status.Errorf(codes.Internal, "%v", err)
		}
		now := time.Now()
		if isPastSettlement(&opt.SettlementDate, now) {
			return status.Error(codes.FailedPrecondition, "option past settlement")
		}

		// Spot price comes from the underlying stock's listing — same source
		// the options grid uses for `shared_price`. No listing means no spot,
		// so we can't decide ITM/OTM and refuse the exercise.
		var listing Listing
		if err := tx.Where("stock_id = ?", opt.StockID).First(&listing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return status.Error(codes.FailedPrecondition, "underlying stock has no listing")
			}
			return status.Errorf(codes.Internal, "%v", err)
		}
		spot := listing.Price
		var ex Exchange
		if err := tx.First(&ex, listing.ExchangeID).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}

		placerID, err := resolvePlacerForCaller(tx, caller)
		if err != nil {
			return err
		}
		var h Holding
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("placer_id = ? AND option_id = ?", placerID, opt.ID).
			First(&h).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return status.Error(codes.FailedPrecondition, "option not held by caller")
		}
		if err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		if h.Amount <= 0 {
			return status.Error(codes.FailedPrecondition, "option not held by caller")
		}

		// Spec p.62: payout per contract is intrinsic value × contract_size,
		// minus the premium the holder paid up front. This codebase keeps
		// options at contract_size 1 (resolveInstrument) so the formula
		// collapses to (intrinsic − premium) × quantity. Premium is debited
		// per-contract because that's how it was paid at acquisition.
		var intrinsic int64
		switch opt.OptionType {
		case OptionCall:
			intrinsic = spot - opt.StrikePrice
		case OptionPut:
			intrinsic = opt.StrikePrice - spot
		default:
			return status.Error(codes.Internal, "unknown option type")
		}
		if intrinsic <= 0 {
			return status.Error(codes.FailedPrecondition, "option is out of the money")
		}
		payoutInstr := (intrinsic - opt.Premium) * h.Amount
		if payoutInstr <= 0 {
			return status.Error(codes.FailedPrecondition, "option payout would be non-positive after premium")
		}

		// Cross-currency credit: convert the instrument-currency payout into
		// the account's currency using the same RSD-anchored rates as the
		// executor. Sell-side rounding (floor) so the bank doesn't pay an
		// extra penny on conversion.
		creditAcc := payoutInstr
		if acc.Currency != ex.Currency {
			rateAcc, err := s.bank.GetExchangeRateToRSD(acc.Currency)
			if err != nil {
				return status.Errorf(codes.Internal, "%v", err)
			}
			rateInstr, err := s.bank.GetExchangeRateToRSD(ex.Currency)
			if err != nil {
				return status.Errorf(codes.Internal, "%v", err)
			}
			creditAcc = int64(math.Floor(float64(payoutInstr) * rateInstr / rateAcc))
		}

		if err := debitSystemAccount(tx, ex.Currency, payoutInstr); err != nil {
			return err
		}
		if err := creditPlacer(tx, req.AccountNumber, creditAcc); err != nil {
			return err
		}

		// Holding is fully consumed by exercise — partial exercise isn't a
		// concept the spec exposes. Delete rather than zero out so the
		// portfolio listing doesn't keep showing a stale 0-amount row.
		if err := tx.Where("id = ?", h.ID).Delete(&Holding{}).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}

		payout = creditAcc
		qty = h.Amount
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &tradingpb.ExerciseOptionResponse{Payout: payout, Quantity: qty}, nil
}
