package bank

import (
	"context"
	"errors"
	"strings"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func validateCreateAccountInput(name string, owner int64, currency string, ownerType string, accountType string, maintainanceCost int64, dailyLimit int64, monthlyLimit int64, createdBy int64, validUntil int64) error {
	if strings.TrimSpace(name) == "" {
		return status.Error(codes.InvalidArgument, "name is required")
	}
	if owner <= 0 {
		return status.Error(codes.InvalidArgument, "owner must be greater than zero")
	}
	if createdBy <= 0 {
		return status.Error(codes.InvalidArgument, "created_by must be greater than zero")
	}
	if strings.TrimSpace(currency) == "" {
		return status.Error(codes.InvalidArgument, "currency is required")
	}
	if ownerType != string(Personal) && ownerType != string(Business) {
		return status.Error(codes.InvalidArgument, "owner_type must be one of personal or business")
	}
	if accountType != string(Checking) && accountType != string(Foreign) {
		return status.Error(codes.InvalidArgument, "account_type must be one of checking or foreign")
	}
	if maintainanceCost < 0 {
		return status.Error(codes.InvalidArgument, "maintainance_cost must be greater than or equal to zero")
	}
	if dailyLimit < 0 {
		return status.Error(codes.InvalidArgument, "daily_limit must be greater than or equal to zero")
	}
	if monthlyLimit < 0 {
		return status.Error(codes.InvalidArgument, "monthly_limit must be greater than or equal to zero")
	}
	if validUntil != 0 && !time.Unix(validUntil, 0).After(time.Now()) {
		return status.Error(codes.InvalidArgument, "valid_until must be in the future")
	}
	return nil
}

func (s *Server) CreateAccount(_ context.Context, req *bankpb.CreateAccountRequest) (*bankpb.CreateAccountResponse, error) {
	name := strings.TrimSpace(req.Name)
	currency := strings.TrimSpace(req.Currency)
	ownerType := strings.TrimSpace(strings.ToLower(req.OwnerType))
	accountType := strings.TrimSpace(strings.ToLower(req.AccountType))

	if err := validateCreateAccountInput(
		name,
		req.Owner,
		currency,
		ownerType,
		accountType,
		req.MaintainanceCost,
		req.DailyLimit,
		req.MonthlyLimit,
		req.CreatedBy,
		req.ValidUntil,
	); err != nil {
		return nil, err
	}

	account := Account{
		Name:              name,
		Owner:             req.Owner,
		Currency:          currency,
		Owner_type:        owner_type(ownerType),
		Account_type:      account_type(accountType),
		Maintainance_cost: req.MaintainanceCost,
		Daily_limit:       req.DailyLimit,
		Monthly_limit:     req.MonthlyLimit,
		Created_by:        req.CreatedBy,
	}
	if req.ValidUntil != 0 {
		account.Valid_until = time.Unix(req.ValidUntil, 0)
	}

	created, err := s.CreateAccountRecord(account)
	if err != nil {
		switch {
		case errors.Is(err, ErrAccountOwnerNotFound):
			return nil, status.Error(codes.InvalidArgument, "owner does not exist")
		case errors.Is(err, ErrAccountCreatorNotFound):
			return nil, status.Error(codes.InvalidArgument, "creator does not exist")
		case errors.Is(err, ErrAccountCurrencyNotFound):
			return nil, status.Error(codes.InvalidArgument, "currency does not exist")
		case errors.Is(err, ErrAccountNumberGenerationFailed):
			return nil, status.Error(codes.Internal, "account number generation failed")
		default:
			return nil, status.Error(codes.Internal, "account creation failed")
		}
	}

	return &bankpb.CreateAccountResponse{
		Valid:         true,
		AccountNumber: created.Number,
		Error:         "",
	}, nil
}
