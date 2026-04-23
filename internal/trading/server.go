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

	// Margin orders require the `margin_trading` permission for employee
	// placers (spec p.56). Checked up front so clients skip the DB round-trip
	// and the denial surfaces before we touch the DB.
	if req.Margin && caller.IsEmployee {
		if !callerHasMarginPermission(s.db, caller.Email) {
			return nil, status.Error(codes.PermissionDenied, "margin_trading permission required")
		}
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

	info, err := s.resolveInstrument(req)
	if err != nil {
		return nil, err
	}

	// Cross-currency orders go through Menjačnica (spec pp.27, 57). Rates are
	// loaded once here so downstream math (margin eligibility, commission
	// conversion) uses a consistent snapshot.
	var rateAccRSD, rateInstrRSD float64 = 1, 1
	if acc.Currency != info.Currency {
		if rateAccRSD, err = s.bank.GetExchangeRateToRSD(acc.Currency); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to load exchange rate for %s: %v", acc.Currency, err)
		}
		if rateInstrRSD, err = s.bank.GetExchangeRateToRSD(info.Currency); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to load exchange rate for %s: %v", info.Currency, err)
		}
	}

	now := time.Now()
	// after_hours is an exchange-clock concept; forex pairs have no exchange
	// and always leave the flag false.
	afterHours := info.Exchange != nil && IsAfterHours(*info.Exchange, now)

	// Approximate-price inputs (spec p.57). We keep PricePerUnit=0 on market
	// orders so the execution engine (#189) re-reads the quote at fill time,
	// but approval math still needs a concrete number — that's what
	// approvalPricePerUnit provides.
	approvalPPU := approvalPricePerUnit(orderType, req, info.MarketPrice)
	approxNative := info.ContractSize * approvalPPU * req.Quantity
	approxRSD, err := s.approxPriceRSD(info.Currency, info.ContractSize, approvalPPU, req.Quantity)
	if err != nil {
		return nil, err
	}
	pastSettlement := isPastSettlement(info.SettlementDate, now)
	commission := computeCommission(orderType, approxNative)

	order := Order{
		OrderType:         orderType,
		Direction:         direction,
		Quantity:          req.Quantity,
		ContractSize:      info.ContractSize,
		PricePerUnit:      marketReferencePrice(orderType, req),
		RemainingPortions: req.Quantity,
		AfterHours:        afterHours,
		AllOrNone:         req.AllOrNone,
		Margin:            req.Margin,
		Commission:        commission,
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
		role := roleClient
		var limits agentLimits
		var employeeID int64

		if caller.IsClient {
			id := caller.ClientID
			placer.ClientID = &id
		} else if caller.IsEmployee {
			if caller.Email == "" {
				return status.Error(codes.Internal, "employee email missing from caller")
			}
			empID, r, lim, roleErr := resolveEmployeeRole(tx, caller.Email)
			if roleErr != nil {
				return roleErr
			}
			employeeID = empID
			role = r
			limits = lim
			placer.EmployeeID = &empID
		} else {
			return status.Error(codes.PermissionDenied, "caller is neither client nor employee")
		}

		order.Status = decideOrderStatus(role, limits, approxRSD, pastSettlement)

		// Margin eligibility runs only for orders that would actually debit
		// (so past-settlement declines skip it). Clients qualify via an
		// approved loan with remaining debt > IMC, otherwise both clients and
		// employees fall back to the account's balance (spec p.56).
		if order.Margin && order.Status != StatusDeclined {
			var clientID int64
			if caller.IsClient {
				clientID = caller.ClientID
			}
			// Margin's native-currency check compares against the instrument
			// currency, so cross-currency balances convert through RSD first.
			debitBalance := acc.Balance
			if acc.Currency != info.Currency {
				debitBalance = int64(float64(acc.Balance) * rateAccRSD / rateInstrRSD)
			}
			if err := s.checkMarginEligibility(tx, clientID, caller.IsClient, debitBalance, info, req.Quantity); err != nil {
				return err
			}
		}

		if err := tx.Create(&placer).Error; err != nil {
			return err
		}
		order.PlacerID = placer.ID
		if err := tx.Create(&order).Error; err != nil {
			return err
		}

		// Commission is reserved at placement (spec pp. 51–52): debit the
		// placer's account and credit the bank's fee pool. Cross-currency
		// orders convert through RSD and, for client placers, additionally
		// charge the Menjačnica commission (spec pp.27, 57). Declined orders
		// (past settlement) skip the whole charge.
		if order.Status != StatusDeclined {
			plan := planCommissionCharge(
				req.AccountNumber, acc.Currency, info.Currency,
				commission, rateAccRSD, rateInstrRSD, caller.IsClient,
			)
			if err := chargeCommission(tx, plan); err != nil {
				return err
			}
		}

		// Agent self-approved orders consume daily limit immediately so a
		// follow-on order sees the updated headroom. Pending orders stay out
		// of used_limit until the supervisor approves them (#204).
		if role == roleAgent && order.Status == StatusApproved {
			if err := tx.Table("employees").
				Where("id = ?", employeeID).
				Update("used_limit", gorm.Expr("used_limit + ?", approxRSD)).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
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
