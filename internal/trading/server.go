package trading

import (
	"context"
	"errors"
	"strings"
	"time"

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
		out = append(out, exchangeToProto(r))
	}
	return &tradingpb.ListExchangesResponse{Exchanges: out}, nil
}

func exchangeToProto(r Exchange) *tradingpb.Exchange {
	return &tradingpb.Exchange{
		Id:             r.ID,
		Name:           r.Name,
		Acronym:        r.Acronym,
		MicCode:        r.MICCode,
		Polity:         r.Polity,
		Currency:       r.Currency,
		TimeZoneOffset: r.TimeZoneOffset,
		OpenTime:       r.OpenTime,
		CloseTime:      r.CloseTime,
		OpenOverride:   r.OpenOverride,
	}
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

	exch, instrumentCurrency, err := s.resolveInstrument(req)
	if err != nil {
		return nil, err
	}
	if acc.Currency != instrumentCurrency {
		return nil, status.Errorf(codes.InvalidArgument,
			"account currency %s does not match instrument currency %s",
			acc.Currency, instrumentCurrency)
	}

	// after_hours is an exchange-clock concept; forex pairs have no exchange
	// and always leave the flag false.
	afterHours := exch != nil && IsAfterHours(*exch, time.Now())

	pricePerUnit := marketReferencePrice(orderType, req)
	order := Order{
		OrderType:         orderType,
		Direction:         direction,
		Status:            StatusPending,
		Quantity:          req.Quantity,
		ContractSize:      1,
		PricePerUnit:      pricePerUnit,
		RemainingPortions: req.Quantity,
		AfterHours:        afterHours,
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

// SetExchangeOpenOverride flips exchanges.open_override so the trading flow
// can be exercised outside real market hours. Supervisor-only: the gateway
// gates the route with `secured("supervisor")`, and we re-check here as
// defense-in-depth (same pattern as UpdateEmployeeTradingLimit).
func (s *Server) SetExchangeOpenOverride(_ context.Context, req *tradingpb.SetExchangeOpenOverrideRequest) (*tradingpb.SetExchangeOpenOverrideResponse, error) {
	if req.ExchangeId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "exchange_id required")
	}
	if !callerIsSupervisor(s.db, req.CallerEmail) {
		return nil, status.Error(codes.PermissionDenied, "only admins and supervisors may toggle open_override")
	}

	var exch Exchange
	if err := s.db.First(&exch, req.ExchangeId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "exchange not found")
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if err := s.db.Model(&exch).Update("open_override", req.OpenOverride).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	exch.OpenOverride = req.OpenOverride

	return &tradingpb.SetExchangeOpenOverrideResponse{Exchange: exchangeToProto(exch)}, nil
}

// callerIsSupervisor checks whether the given employee email has `admin` or
// `supervisor` in employee_permissions. Trading lives in the bank process so
// it can hit the same DB directly rather than calling the user service.
func callerIsSupervisor(db *gorm.DB, email string) bool {
	if strings.TrimSpace(email) == "" {
		return false
	}
	var count int64
	err := db.Table("employees").
		Joins("JOIN employee_permissions ep ON ep.employee_id = employees.id").
		Joins("JOIN permissions p ON p.id = ep.permission_id").
		Where("employees.email = ? AND p.name IN (?)", email, []string{"admin", "supervisor"}).
		Count(&count).Error
	if err != nil {
		return false
	}
	return count > 0
}
