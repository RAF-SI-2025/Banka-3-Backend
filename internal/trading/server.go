package trading

import (
	"context"
	"errors"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"github.com/RAF-SI-2025/Banka-3-Backend/internal/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type Server struct {
	tradingpb.UnimplementedTradingServiceServer
	db   *gorm.DB
	bank *bank.Server
}

// NewServer wires trading to the bank server running in the same process —
// trading reuses bank's auth (ResolveCaller) and account lookups since orders
// always debit a bank account.
func NewServer(db *gorm.DB, bankSrv *bank.Server) *Server {
	return &Server{db: db, bank: bankSrv}
}

func (s *Server) ListExchanges(_ context.Context, _ *tradingpb.ListExchangesRequest) (*tradingpb.ListExchangesResponse, error) {
	rows, err := s.ListExchangesRecord()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	out := make([]*tradingpb.Exchange, 0, len(rows))
	for _, r := range rows {
		out = append(out, &tradingpb.Exchange{
			Id:             r.ID,
			Name:           r.Name,
			Acronym:        r.Acronym,
			MicCode:        r.MICCode,
			Polity:         r.Polity,
			Currency:       r.Currency,
			TimeZoneOffset: r.TimeZoneOffset,
			OpenOverride:   r.OpenOverride,
		})
	}
	return &tradingpb.ListExchangesResponse{Exchanges: out}, nil
}

func (s *Server) ListListings(_ context.Context, _ *tradingpb.ListListingsRequest) (*tradingpb.ListListingsResponse, error) {
	rows, err := s.ListListingsRecord()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	out := make([]*tradingpb.Listing, 0, len(rows))
	for _, r := range rows {
		var stockID, futureID int64
		if r.StockID != nil {
			stockID = *r.StockID
		}
		if r.FutureID != nil {
			futureID = *r.FutureID
		}
		out = append(out, &tradingpb.Listing{
			Id:              r.ID,
			ExchangeId:      r.ExchangeID,
			StockId:         stockID,
			FutureId:        futureID,
			Price:           r.Price,
			AskPrice:        r.AskPrice,
			BidPrice:        r.BidPrice,
			LastRefreshUnix: r.LastRefresh.Unix(),
		})
	}
	return &tradingpb.ListListingsResponse{Listings: out}, nil
}

func (s *Server) CreateOrder(ctx context.Context, req *tradingpb.CreateOrderRequest) (*tradingpb.CreateOrderResponse, error) {
	if req.Quantity <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be positive")
	}
	if req.AccountNumber == "" {
		return nil, status.Error(codes.InvalidArgument, "account_number required")
	}

	orderType, err := parseOrderType(req.OrderType)
	if err != nil {
		return nil, err
	}
	direction, err := parseDirection(req.Direction)
	if err != nil {
		return nil, err
	}
	if err := validatePriceFields(orderType, req.LimitPrice, req.StopPrice); err != nil {
		return nil, err
	}

	caller, err := s.bank.ResolveCaller(ctx)
	if err != nil {
		return nil, err
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

	instrumentCurrency, err := s.resolveInstrumentCurrency(req)
	if err != nil {
		return nil, err
	}
	if acc.Currency != instrumentCurrency {
		return nil, status.Errorf(codes.InvalidArgument,
			"account currency %s does not match instrument currency %s",
			acc.Currency, instrumentCurrency)
	}

	pricePerUnit := marketReferencePrice(orderType, req)
	order := Order{
		OrderType:         orderType,
		Direction:         direction,
		Status:            StatusPending,
		Quantity:          req.Quantity,
		ContractSize:      1,
		PricePerUnit:      pricePerUnit,
		RemainingPortions: req.Quantity,
		AllOrNone:         req.AllOrNone,
		Margin:            req.Margin,
	}
	if req.ListingId != 0 {
		v := req.ListingId
		order.ListingID = &v
	}
	if req.OptionId != 0 {
		v := req.OptionId
		order.OptionID = &v
	}
	if req.ForexPairId != 0 {
		v := req.ForexPairId
		order.ForexPairID = &v
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		placer := OrderPlacer{}
		if caller.IsClient {
			id := caller.ClientID
			placer.ClientID = &id
		} else if caller.IsEmployee {
			if caller.Email == "" {
				return status.Error(codes.Internal, "employee email missing from caller")
			}
			empID, lookupErr := lookupEmployeeID(tx, caller.Email)
			if lookupErr != nil {
				return lookupErr
			}
			placer.EmployeeID = &empID
		} else {
			return status.Error(codes.PermissionDenied, "caller is neither client nor employee")
		}
		if err := tx.Create(&placer).Error; err != nil {
			return err
		}
		order.PlacerID = placer.ID
		return tx.Create(&order).Error
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	return &tradingpb.CreateOrderResponse{
		OrderId: order.ID,
		Status:  string(order.Status),
	}, nil
}
