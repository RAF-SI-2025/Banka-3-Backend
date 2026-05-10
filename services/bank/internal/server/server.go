// Package server adapts the proto-generated BankService surface to the
// service layer.
package server

import (
	"context"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	bankpb.UnimplementedBankServiceServer
	Svc *service.Service
}

func New(svc *service.Service) *Server { return &Server{Svc: svc} }

// =====================================================================
// Companies
// =====================================================================

func (s *Server) CreateCompany(ctx context.Context, in *bankpb.CreateCompanyRequest) (*bankpb.Company, error) {
	c, err := s.Svc.CreateCompany(ctx, service.CreateCompanyInput{
		Name:          in.GetName(),
		RegistryID:    in.GetRegistryId(),
		TaxID:         in.GetTaxId(),
		ActivityCode:  in.GetActivityCode(),
		Address:       in.GetAddress(),
		OwnerClientID: in.GetOwnerClientId(),
	})
	if err != nil {
		return nil, err
	}
	return companyToProto(c), nil
}

func (s *Server) ListCompanies(ctx context.Context, in *bankpb.ListCompaniesRequest) (*bankpb.ListCompaniesResponse, error) {
	cs, total, err := s.Svc.ListCompanies(ctx, domain.CompanyFilter{
		Name:       in.GetNameQuery(),
		RegistryID: in.GetRegistryIdQuery(),
	}, int(in.GetPage()), int(in.GetPageSize()))
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.Company, 0, len(cs))
	for _, c := range cs {
		out = append(out, companyToProto(c))
	}
	page := int(in.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(in.GetPageSize())
	if pageSize < 1 {
		pageSize = 50
	}
	return &bankpb.ListCompaniesResponse{
		Companies: out,
		Page:      int32(page),
		PageSize:  int32(pageSize),
		Total:     total,
	}, nil
}

func (s *Server) GetCompany(ctx context.Context, in *bankpb.GetCompanyRequest) (*bankpb.Company, error) {
	c, err := s.Svc.GetCompany(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return companyToProto(c), nil
}

func (s *Server) UpdateCompany(ctx context.Context, in *bankpb.UpdateCompanyRequest) (*bankpb.Company, error) {
	c, err := s.Svc.UpdateCompany(ctx, service.UpdateCompanyInput{
		ID:            in.GetId(),
		Name:          in.GetName(),
		ActivityCode:  in.GetActivityCode(),
		Address:       in.GetAddress(),
		OwnerClientID: in.GetOwnerClientId(),
	})
	if err != nil {
		return nil, err
	}
	return companyToProto(c), nil
}

// =====================================================================
// Accounts
// =====================================================================

func (s *Server) CreateAccount(ctx context.Context, in *bankpb.CreateAccountRequest) (*bankpb.Account, error) {
	a, err := s.Svc.CreateAccount(ctx, service.CreateAccountInput{
		OwnerClientID:  in.GetOwnerClientId(),
		CompanyID:      in.GetCompanyId(),
		Kind:           kindFromProto(in.GetKind()),
		Subtype:        subtypeFromProto(in.GetSubtype()),
		Currency:       currencyFromProto(in.GetCurrency()),
		Name:           in.GetName(),
		OpeningBalance: in.GetOpeningBalance(),
		CreateCard:     in.GetCreateCard(),
	})
	if err != nil {
		return nil, err
	}
	return accountToProto(a), nil
}

func (s *Server) ListAccounts(ctx context.Context, in *bankpb.ListAccountsRequest) (*bankpb.ListAccountsResponse, error) {
	as, total, err := s.Svc.ListAccounts(ctx, domain.AccountFilter{
		OwnerClientID: in.GetOwnerClientId(),
		Kind:          kindFromProto(in.GetKind()),
		Currency:      currencyFromProto(in.GetCurrency()),
		Status:        statusFromProto(in.GetStatus()),
	}, int(in.GetPage()), int(in.GetPageSize()))
	if err != nil {
		return nil, err
	}
	out := make([]*bankpb.Account, 0, len(as))
	for _, a := range as {
		out = append(out, accountToProto(a))
	}
	page := int(in.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(in.GetPageSize())
	if pageSize < 1 {
		pageSize = 50
	}
	return &bankpb.ListAccountsResponse{
		Accounts: out,
		Page:     int32(page),
		PageSize: int32(pageSize),
		Total:    total,
	}, nil
}

func (s *Server) GetAccount(ctx context.Context, in *bankpb.GetAccountRequest) (*bankpb.Account, error) {
	a, err := s.Svc.GetAccount(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return accountToProto(a), nil
}

func (s *Server) UpdateAccountLimits(ctx context.Context, in *bankpb.UpdateAccountLimitsRequest) (*bankpb.Account, error) {
	a, err := s.Svc.UpdateAccountLimits(ctx, in.GetId(), in.GetDailyLimit(), in.GetMonthlyLimit())
	if err != nil {
		return nil, err
	}
	return accountToProto(a), nil
}

func (s *Server) UpdateAccountName(ctx context.Context, in *bankpb.UpdateAccountNameRequest) (*bankpb.Account, error) {
	a, err := s.Svc.UpdateAccountName(ctx, in.GetId(), in.GetName())
	if err != nil {
		return nil, err
	}
	return accountToProto(a), nil
}

func (s *Server) SetAccountStatus(ctx context.Context, in *bankpb.SetAccountStatusRequest) (*bankpb.Account, error) {
	a, err := s.Svc.SetAccountStatus(ctx, in.GetId(), statusFromProto(in.GetStatus()))
	if err != nil {
		return nil, err
	}
	return accountToProto(a), nil
}

func (s *Server) GetSystemAccount(ctx context.Context, in *bankpb.GetSystemAccountRequest) (*bankpb.Account, error) {
	a, err := s.Svc.GetSystemAccount(ctx, currencyFromProto(in.GetCurrency()))
	if err != nil {
		return nil, err
	}
	return accountToProto(a), nil
}

// =====================================================================
// Conversions
// =====================================================================

func companyToProto(c *domain.Company) *bankpb.Company {
	return &bankpb.Company{
		Id:            c.ID,
		Name:          c.Name,
		RegistryId:    c.RegistryID,
		TaxId:         c.TaxID,
		ActivityCode:  c.ActivityCode,
		Address:       c.Address,
		OwnerClientId: c.OwnerClientID,
		CreatedAt:     timestamppb.New(c.CreatedAt),
		UpdatedAt:     timestamppb.New(c.UpdatedAt),
	}
}

func accountToProto(a *domain.Account) *bankpb.Account {
	out := &bankpb.Account{
		Id:                    a.ID,
		Number:                a.Number,
		Name:                  a.Name,
		OwnerClientId:         a.OwnerClientID,
		CompanyId:             a.CompanyID,
		CreatedByEmployeeId:   a.CreatedByEmployeeID,
		Kind:                  kindToProto(a.Kind),
		Subtype:               subtypeToProto(a.Subtype),
		Currency:              currencyToProto(a.Currency),
		Status:                statusToProto(a.Status),
		Balance:               a.Balance,
		AvailableBalance:      a.AvailableBalance,
		MaintenanceFee:        a.MaintenanceFee,
		DailyLimit:            a.DailyLimit,
		MonthlyLimit:          a.MonthlyLimit,
		DailySpent:            a.DailySpent,
		MonthlySpent:          a.MonthlySpent,
		CreatedAt:             timestamppb.New(a.CreatedAt),
		UpdatedAt:             timestamppb.New(a.UpdatedAt),
	}
	if a.ExpiresAt != nil {
		out.ExpiresAt = timestamppb.New(*a.ExpiresAt)
	}
	return out
}

// =====================================================================
// Enum conversions
// =====================================================================

func kindToProto(k domain.AccountKind) bankpb.AccountKind {
	switch k {
	case domain.KindPersonalCheckingRSD:
		return bankpb.AccountKind_ACCOUNT_KIND_PERSONAL_CHECKING_RSD
	case domain.KindPersonalFX:
		return bankpb.AccountKind_ACCOUNT_KIND_PERSONAL_FX
	case domain.KindBusinessCheckingRSD:
		return bankpb.AccountKind_ACCOUNT_KIND_BUSINESS_CHECKING_RSD
	case domain.KindBusinessFX:
		return bankpb.AccountKind_ACCOUNT_KIND_BUSINESS_FX
	case domain.KindSystem:
		return bankpb.AccountKind_ACCOUNT_KIND_SYSTEM
	case domain.KindForexBook:
		return bankpb.AccountKind_ACCOUNT_KIND_FOREX_BOOK
	case domain.KindStateTax:
		return bankpb.AccountKind_ACCOUNT_KIND_STATE_TAX
	}
	return bankpb.AccountKind_ACCOUNT_KIND_UNSPECIFIED
}

func kindFromProto(k bankpb.AccountKind) domain.AccountKind {
	switch k {
	case bankpb.AccountKind_ACCOUNT_KIND_PERSONAL_CHECKING_RSD:
		return domain.KindPersonalCheckingRSD
	case bankpb.AccountKind_ACCOUNT_KIND_PERSONAL_FX:
		return domain.KindPersonalFX
	case bankpb.AccountKind_ACCOUNT_KIND_BUSINESS_CHECKING_RSD:
		return domain.KindBusinessCheckingRSD
	case bankpb.AccountKind_ACCOUNT_KIND_BUSINESS_FX:
		return domain.KindBusinessFX
	case bankpb.AccountKind_ACCOUNT_KIND_SYSTEM:
		return domain.KindSystem
	case bankpb.AccountKind_ACCOUNT_KIND_FOREX_BOOK:
		return domain.KindForexBook
	case bankpb.AccountKind_ACCOUNT_KIND_STATE_TAX:
		return domain.KindStateTax
	}
	return ""
}

func subtypeToProto(s domain.AccountSubtype) bankpb.AccountSubtype {
	switch s {
	case domain.SubtypeStandard:
		return bankpb.AccountSubtype_ACCOUNT_SUBTYPE_STANDARD
	case domain.SubtypeSavings:
		return bankpb.AccountSubtype_ACCOUNT_SUBTYPE_SAVINGS
	case domain.SubtypePensioner:
		return bankpb.AccountSubtype_ACCOUNT_SUBTYPE_PENSIONER
	case domain.SubtypeYouth:
		return bankpb.AccountSubtype_ACCOUNT_SUBTYPE_YOUTH
	case domain.SubtypeStudent:
		return bankpb.AccountSubtype_ACCOUNT_SUBTYPE_STUDENT
	case domain.SubtypeUnemployed:
		return bankpb.AccountSubtype_ACCOUNT_SUBTYPE_UNEMPLOYED
	case domain.SubtypeDOO:
		return bankpb.AccountSubtype_ACCOUNT_SUBTYPE_DOO
	case domain.SubtypeAD:
		return bankpb.AccountSubtype_ACCOUNT_SUBTYPE_AD
	case domain.SubtypeFoundation:
		return bankpb.AccountSubtype_ACCOUNT_SUBTYPE_FOUNDATION
	}
	return bankpb.AccountSubtype_ACCOUNT_SUBTYPE_UNSPECIFIED
}

func subtypeFromProto(s bankpb.AccountSubtype) domain.AccountSubtype {
	switch s {
	case bankpb.AccountSubtype_ACCOUNT_SUBTYPE_STANDARD:
		return domain.SubtypeStandard
	case bankpb.AccountSubtype_ACCOUNT_SUBTYPE_SAVINGS:
		return domain.SubtypeSavings
	case bankpb.AccountSubtype_ACCOUNT_SUBTYPE_PENSIONER:
		return domain.SubtypePensioner
	case bankpb.AccountSubtype_ACCOUNT_SUBTYPE_YOUTH:
		return domain.SubtypeYouth
	case bankpb.AccountSubtype_ACCOUNT_SUBTYPE_STUDENT:
		return domain.SubtypeStudent
	case bankpb.AccountSubtype_ACCOUNT_SUBTYPE_UNEMPLOYED:
		return domain.SubtypeUnemployed
	case bankpb.AccountSubtype_ACCOUNT_SUBTYPE_DOO:
		return domain.SubtypeDOO
	case bankpb.AccountSubtype_ACCOUNT_SUBTYPE_AD:
		return domain.SubtypeAD
	case bankpb.AccountSubtype_ACCOUNT_SUBTYPE_FOUNDATION:
		return domain.SubtypeFoundation
	}
	return domain.SubtypeUnspecified
}

func currencyToProto(c domain.Currency) bankpb.Currency {
	switch c {
	case domain.CurrencyRSD:
		return bankpb.Currency_CURRENCY_RSD
	case domain.CurrencyEUR:
		return bankpb.Currency_CURRENCY_EUR
	case domain.CurrencyCHF:
		return bankpb.Currency_CURRENCY_CHF
	case domain.CurrencyUSD:
		return bankpb.Currency_CURRENCY_USD
	case domain.CurrencyGBP:
		return bankpb.Currency_CURRENCY_GBP
	case domain.CurrencyJPY:
		return bankpb.Currency_CURRENCY_JPY
	case domain.CurrencyCAD:
		return bankpb.Currency_CURRENCY_CAD
	case domain.CurrencyAUD:
		return bankpb.Currency_CURRENCY_AUD
	}
	return bankpb.Currency_CURRENCY_UNSPECIFIED
}

func currencyFromProto(c bankpb.Currency) domain.Currency {
	switch c {
	case bankpb.Currency_CURRENCY_RSD:
		return domain.CurrencyRSD
	case bankpb.Currency_CURRENCY_EUR:
		return domain.CurrencyEUR
	case bankpb.Currency_CURRENCY_CHF:
		return domain.CurrencyCHF
	case bankpb.Currency_CURRENCY_USD:
		return domain.CurrencyUSD
	case bankpb.Currency_CURRENCY_GBP:
		return domain.CurrencyGBP
	case bankpb.Currency_CURRENCY_JPY:
		return domain.CurrencyJPY
	case bankpb.Currency_CURRENCY_CAD:
		return domain.CurrencyCAD
	case bankpb.Currency_CURRENCY_AUD:
		return domain.CurrencyAUD
	}
	return ""
}

func statusToProto(s domain.AccountStatus) bankpb.AccountStatus {
	switch s {
	case domain.AccountActive:
		return bankpb.AccountStatus_ACCOUNT_STATUS_ACTIVE
	case domain.AccountInactive:
		return bankpb.AccountStatus_ACCOUNT_STATUS_INACTIVE
	}
	return bankpb.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED
}

func statusFromProto(s bankpb.AccountStatus) domain.AccountStatus {
	switch s {
	case bankpb.AccountStatus_ACCOUNT_STATUS_ACTIVE:
		return domain.AccountActive
	case bankpb.AccountStatus_ACCOUNT_STATUS_INACTIVE:
		return domain.AccountInactive
	}
	return ""
}
