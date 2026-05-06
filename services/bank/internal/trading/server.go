package trading

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/trading"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/bank"
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
		ClosedOverride: r.ClosedOverride,
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

	// Spec pp.50, 59: clients may only trade stocks and futures. Options and
	// forex are actuary-only. Block at placement so a client who guesses an
	// option_id/forex_pair_id off another endpoint can't slip a trade through.
	if caller.IsClient {
		if req.OptionId != 0 {
			return nil, status.Error(codes.PermissionDenied, "options are available to employees only")
		}
		if req.ForexPairId != 0 {
			return nil, status.Error(codes.PermissionDenied, "forex is available to employees only")
		}
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
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Errorf(codes.FailedPrecondition, "valuta računa (%s) nije podržana za trgovanje", acc.Currency)
			}
			return nil, status.Errorf(codes.Internal, "failed to load exchange rate for %s: %v", acc.Currency, err)
		}
		if rateInstrRSD, err = s.bank.GetExchangeRateToRSD(info.Currency); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Errorf(codes.FailedPrecondition, "valuta hartije (%s) nije podržana za trgovanje", info.Currency)
			}
			return nil, status.Errorf(codes.Internal, "failed to load exchange rate for %s: %v", info.Currency, err)
		}
	}

	now := time.Now()
	// after_hours is an exchange-clock concept; forex pairs have no exchange
	// and always leave the flag false. Closed-exchange market orders queue
	// here and pick up the executor's 30-min after-hours bonus once IsOpen
	// flips back (spec #46): the prior hard reject contradicted the spec.
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

	// Predict whether this order would route to pending (agent over limit /
	// need_approval), so we can skip the pre-flight funds check for it — a
	// pending order doesn't debit until a supervisor approves, and the funds
	// check will run again at that point.
	predictedPending := false
	if caller.IsEmployee && caller.Email != "" {
		_, predRole, predLimits, predErr := resolveEmployeeRole(s.db, caller.Email)
		if predErr == nil {
			predictedPending = decideOrderStatus(predRole, predLimits, approxRSD, pastSettlement) == StatusPending
		}
	}

	// Pre-flight funds check for non-margin BUYs (spec p.57 / #43): reject at
	// placement instead of waiting for execution to fail. Margin orders run
	// their own eligibility check below; past-settlement orders auto-decline
	// without a debit, so both skip this guard. Pending orders also skip — the
	// funds requirement is re-evaluated when the supervisor approves.
	if direction == DirectionBuy && !req.Margin && !pastSettlement && !predictedPending {
		approxAcc := approxNative
		commissionDebit := commission
		if acc.Currency != info.Currency {
			approxAcc = int64(math.Ceil(float64(approxNative) * rateInstrRSD / rateAccRSD))
			commissionDebit = int64(math.Ceil(float64(commission) * rateInstrRSD / rateAccRSD))
			if caller.IsClient {
				commissionDebit += (commissionDebit*menjacnicaCommissionPermille + 999) / 1000
			}
		}
		if acc.Balance < approxAcc+commissionDebit {
			return nil, status.Error(codes.FailedPrecondition, "insufficient funds for order")
		}
	}

	order := Order{
		OrderType:         orderType,
		Direction:         direction,
		AccountNumber:     req.AccountNumber,
		Quantity:          req.Quantity,
		ContractSize:      info.ContractSize,
		PricePerUnit:      marketReferencePrice(orderType, req),
		StopPrice:         stopTriggerPrice(orderType, req),
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
		role := roleClient
		var limits agentLimits
		var employeeID int64
		var clientPlacer *int64
		var employeePlacer *int64

		if caller.IsClient {
			id := caller.ClientID
			clientPlacer = &id
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
			employeePlacer = &empID
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

		placerID, err := findOrCreatePlacer(tx, clientPlacer, employeePlacer)
		if err != nil {
			return err
		}
		order.PlacerID = placerID
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

// SetExchangeClosedOverride mirrors SetExchangeOpenOverride for the inverse
// toggle: closed_override forces IsOpen=false regardless of clock and
// open_override. Lets cypress drive closed-market flows during NY business
// hours (#46) without time-control.
func (s *Server) SetExchangeClosedOverride(_ context.Context, req *tradingpb.SetExchangeClosedOverrideRequest) (*tradingpb.SetExchangeClosedOverrideResponse, error) {
	if req.ExchangeId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "exchange_id required")
	}
	if !callerIsSupervisor(s.db, req.CallerEmail) {
		return nil, status.Error(codes.PermissionDenied, "only admins and supervisors may toggle closed_override")
	}

	var exch Exchange
	if err := s.db.First(&exch, req.ExchangeId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "exchange not found")
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if err := s.db.Model(&exch).Update("closed_override", req.ClosedOverride).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	exch.ClosedOverride = req.ClosedOverride

	return &tradingpb.SetExchangeClosedOverrideResponse{Exchange: exchangeToProto(exch)}, nil
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
