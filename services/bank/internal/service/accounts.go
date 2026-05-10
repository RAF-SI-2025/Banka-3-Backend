package service

import (
	"context"
	"errors"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/account"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// CreateAccountInput is the validated payload for CreateAccount.
type CreateAccountInput struct {
	OwnerClientID  string
	CompanyID      string // empty unless Kind is business
	Kind           domain.AccountKind
	Subtype        domain.AccountSubtype
	Currency       domain.Currency
	Name           string
	OpeningBalance string // decimal string; "" → "0"
	CreateCard     bool   // spec p.12: optional companion card
}

// CreateAccount mints a new account on behalf of a Klijent. Spec p.11:
// only an authenticated employee can create accounts.
//
// When CreateCard is true the account is opened with a companion debit
// card (Visa, daily-limit-matched ceiling) — flow.pdf P2 mandates that
// the card shows up in the cards list afterwards.
func (s *Service) CreateAccount(ctx context.Context, in CreateAccountInput) (*domain.Account, error) {
	if err := s.requirePermission(ctx, permissions.AccountWrite); err != nil {
		return nil, err
	}
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.validateCreateAccount(in); err != nil {
		return nil, err
	}

	number, err := account.Generate(s.Cfg.BankCode, s.Cfg.Branch, kindAndSubtypeToAccountType(in.Kind, in.Subtype))
	if err != nil {
		return nil, apperr.Internal("generate account number", err)
	}

	opening := in.OpeningBalance
	if strings.TrimSpace(opening) == "" {
		opening = "0"
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = defaultAccountName(in.Kind, in.Currency)
	}

	a := &domain.Account{
		Number:              number,
		Name:                name,
		OwnerClientID:       in.OwnerClientID,
		CompanyID:           in.CompanyID,
		CreatedByEmployeeID: p.UserID,
		Kind:                in.Kind,
		Subtype:             in.Subtype,
		Currency:            in.Currency,
		Status:              domain.AccountActive,
		Balance:             opening,
		AvailableBalance:    opening,
		MaintenanceFee:      DefaultMaintenanceFee(in.Kind, in.Subtype, in.Currency),
		DailyLimit:          DefaultDailyLimit(in.Currency),
		MonthlyLimit:        DefaultMonthlyLimit(in.Currency),
		DailySpent:          "0",
		MonthlySpent:        "0",
	}
	created, err := s.Store.CreateAccount(ctx, a)
	if err != nil {
		return nil, err
	}

	// Account-opened email (spec E2E "Sistem kreira račun i klijent
	// dobija email obaveštenje"). Best-effort: failures must not roll
	// back the account, so go through the same notify helper card
	// status uses.
	s.notifyAccountCreated(ctx, created)

	// Companion card: spec p.12 + flow.pdf P2 — when the option is
	// selected, an active card must be visible in the client's cards
	// list right after account opening.
	if in.CreateCard {
		if _, _, err := s.CreateCard(ctx, CreateCardInput{
			AccountID: created.ID,
			Brand:     domain.BrandVisa,
			Name:      defaultCompanionCardName(in.Kind),
			CardLimit: created.DailyLimit,
		}); err != nil {
			return nil, err
		}
	}
	return created, nil
}

func defaultCompanionCardName(k domain.AccountKind) string {
	if k.IsBusiness() {
		return "Poslovna kartica"
	}
	return "Lična kartica"
}

// DefaultMaintenanceFee returns the per-month fee for a freshly opened
// account, in the account's currency. Spec p.12 example shows
// 255.00 RSD for a standard RSD account; the FX example doesn't list a
// "Održavanje računa" row at all, so FX accounts go in fee-free.
//
// The fee table below is our concrete fill-in for the gaps in the spec
// (it only shows one example value). Common Serbian banking practice
// is to wave the fee for student / pensioner / unemployed accounts; we
// follow that convention.
func DefaultMaintenanceFee(kind domain.AccountKind, subtype domain.AccountSubtype, currency domain.Currency) string {
	// FX accounts: no monthly fee per spec p.13 (no fee row in the
	// devizni table).
	if currency != domain.CurrencyRSD {
		return "0"
	}
	switch kind {
	case domain.KindPersonalCheckingRSD:
		switch subtype {
		case domain.SubtypeStandard:
			return "255" // matches spec p.12 example
		case domain.SubtypePensioner:
			return "100"
		case domain.SubtypeYouth, domain.SubtypeStudent, domain.SubtypeUnemployed,
			domain.SubtypeSavings:
			return "0"
		}
	case domain.KindBusinessCheckingRSD:
		switch subtype {
		case domain.SubtypeDOO:
			return "500"
		case domain.SubtypeAD:
			return "800"
		case domain.SubtypeFoundation:
			return "200"
		}
	}
	return "0"
}

// DefaultDailyLimit / DefaultMonthlyLimit seed the per-spec "Dnevni
// limit" / "Mesečni limit" fields with the values from spec p.12-13.
// Employees can adjust later via UpdateAccountLimits.
func DefaultDailyLimit(currency domain.Currency) string {
	if currency == domain.CurrencyRSD {
		return "250000" // 250.000,00 RSD per spec p.12
	}
	return "5000" // 5.000,00 X for FX (spec p.13 EUR example)
}

func DefaultMonthlyLimit(currency domain.Currency) string {
	if currency == domain.CurrencyRSD {
		return "1000000" // 1.000.000,00 RSD per spec p.12
	}
	return "20000" // 20.000,00 X for FX
}

// GetAccount returns one by ID. Clients can only fetch their own;
// employees with AccountRead can fetch any.
func (s *Service) GetAccount(ctx context.Context, id string) (*domain.Account, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.Store.GetAccountByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !canSeeAccount(p, a) {
		return nil, apperr.PermissionDenied("nedovoljne permisije")
	}
	return a, nil
}

// ListAccounts honors the same scoping rule as GetAccount: clients see
// their own, employees with AccountRead see whatever the filter asks
// for.
func (s *Service) ListAccounts(ctx context.Context, f domain.AccountFilter, page, pageSize int) ([]*domain.Account, int64, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, 0, err
	}
	if p.UserKind == auth.KindClient {
		// Force the filter to the caller's own accounts. If they passed
		// owner_client_id explicitly and it doesn't match, that's a
		// permission error rather than a silent re-scope.
		if f.OwnerClientID != "" && f.OwnerClientID != p.UserID {
			return nil, 0, apperr.PermissionDenied("nedovoljne permisije")
		}
		f.OwnerClientID = p.UserID
	} else if !permissions.HasAny(p.Permissions, permissions.AccountRead, permissions.Admin) {
		return nil, 0, apperr.PermissionDenied("nedovoljne permisije")
	}
	// Bank-internal bookkeeping accounts (system menjačnica house,
	// state-tax destination, per-currency forex_book) are reachable
	// via GetSystemAccount / GetAccount(id); they have no owner,
	// name, or subtype to render and would just clutter the employee
	// Računi list. Exclude them unless the caller asked for that
	// kind explicitly.
	if f.Kind == "" {
		f.ExcludeKinds = []domain.AccountKind{
			domain.KindSystem,
			domain.KindStateTax,
			domain.KindForexBook,
		}
	}
	return s.Store.ListAccounts(ctx, f, page, pageSize)
}

func (s *Service) UpdateAccountLimits(ctx context.Context, id, daily, monthly string) (*domain.Account, error) {
	if err := s.requirePermission(ctx, permissions.AccountWrite); err != nil {
		return nil, err
	}
	return s.Store.UpdateAccountLimits(ctx, id, strings.TrimSpace(daily), strings.TrimSpace(monthly))
}

// UpdateAccountName implements the spec p.20 "Promena naziva računa"
// flow. The principal must be the account owner (clients renaming
// their own account); employees with AccountWrite are also allowed
// for back-office corrections. The new name must differ from the
// current one and must not collide with another active account
// belonging to the same client (per spec p.20 validation rules).
func (s *Service) UpdateAccountName(ctx context.Context, id, name string) (*domain.Account, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, apperr.Validation("naziv ne sme biti prazan")
	}
	current, err := s.Store.GetAccountByID(ctx, id)
	if err != nil {
		return nil, err
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return nil, apperr.Unauthenticated("not authenticated")
	}
	isOwner := current.OwnerClientID == p.UserID
	hasEmployeePerm := permissions.Has(p.Permissions, permissions.AccountWrite)
	if !isOwner && !hasEmployeePerm {
		return nil, apperr.PermissionDenied("samo vlasnik računa može menjati naziv")
	}
	if name == current.Name {
		return nil, apperr.Validation("novo ime mora biti različito od trenutnog")
	}
	if current.OwnerClientID != "" {
		taken, err := s.Store.AccountNameTakenByOwner(ctx, current.OwnerClientID, name, id)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, apperr.Conflict("već imate račun sa tim nazivom")
		}
	}
	return s.Store.UpdateAccountName(ctx, id, name)
}

func (s *Service) SetAccountStatus(ctx context.Context, id string, status domain.AccountStatus) (*domain.Account, error) {
	if err := s.requirePermission(ctx, permissions.AccountWrite); err != nil {
		return nil, err
	}
	if status != domain.AccountActive && status != domain.AccountInactive {
		return nil, apperr.Validation("status must be active or inactive")
	}
	return s.Store.SetAccountStatus(ctx, id, status)
}

// GetSystemAccount returns the bank's house account for currency.
// Internal RPC — relies on service-to-service trust.
func (s *Service) GetSystemAccount(ctx context.Context, currency domain.Currency) (*domain.Account, error) {
	if !currency.Supported() {
		return nil, apperr.Validation("unsupported currency")
	}
	return s.Store.GetSystemAccount(ctx, currency)
}

// EnsureSystemAccounts is called once at boot. For each supported
// currency it creates a bank-owned account if one doesn't exist yet.
// Idempotent.
//
// The bank's house accounts are pre-funded with a large notional
// balance so the menjačnica path's intermediate legs never underflow
// — in a real bank these accounts are nostro/vostro positions backed
// by external reserves; for the dev model we just stamp 10^9 of each
// currency so the >= 0 invariant holds across normal traffic.
func (s *Service) EnsureSystemAccounts(ctx context.Context) error {
	const houseFloat = "1000000000.0000" // 1 billion units per currency
	for _, c := range domain.SupportedCurrencies() {
		if _, err := s.Store.GetSystemAccount(ctx, c); err == nil {
			continue
		} else if !isNotFound(err) {
			return err
		}
		number, err := account.Generate(s.Cfg.BankCode, s.Cfg.Branch, account.TypeSystem)
		if err != nil {
			return err
		}
		_, err = s.Store.CreateAccount(ctx, &domain.Account{
			Number:              number,
			Name:                "Sistemski račun (" + string(c) + ")",
			OwnerClientID:       domain.SystemOwnerID,
			CreatedByEmployeeID: domain.SystemOwnerID,
			Kind:                domain.KindSystem,
			Subtype:             domain.SubtypeUnspecified,
			Currency:            c,
			Status:              domain.AccountActive,
			Balance:             houseFloat,
			AvailableBalance:    houseFloat,
			MaintenanceFee:      "0",
			DailyLimit:          "0",
			MonthlyLimit:        "0",
			DailySpent:          "0",
			MonthlySpent:        "0",
		})
		if err != nil {
			return err
		}
		s.Log.Info("seeded system account", "currency", c, "number", number)
	}

	// Spec p.62 capital-gains-tax destination. The state has only an
	// RSD account; the c3 tax cron credits it from this side. Treated
	// like a system account but kept under a separate AccountKind so
	// it doesn't muddy the menjačnica's bank-house lookups.
	if _, err := s.Store.GetStateTaxAccount(ctx); err != nil {
		if !isNotFound(err) {
			return err
		}
		number, err := account.Generate(s.Cfg.BankCode, s.Cfg.Branch, account.TypeSystem)
		if err != nil {
			return err
		}
		_, err = s.Store.CreateAccount(ctx, &domain.Account{
			Number:              number,
			Name:                "Državni račun za porez na kapitalni dobitak",
			OwnerClientID:       domain.StateTaxOwnerID,
			CreatedByEmployeeID: domain.SystemOwnerID,
			Kind:                domain.KindStateTax,
			Subtype:             domain.SubtypeUnspecified,
			Currency:            domain.CurrencyRSD,
			Status:              domain.AccountActive,
			Balance:             "0.0000",
			AvailableBalance:    "0.0000",
			MaintenanceFee:      "0",
			DailyLimit:          "0",
			MonthlyLimit:        "0",
			DailySpent:          "0",
			MonthlySpent:        "0",
		})
		if err != nil {
			return err
		}
		s.Log.Info("seeded state tax account", "number", number)
	}

	// Spec p.42 forex book — one bank-owned account per supported
	// currency, pre-funded as the conceptual "market" counterparty for
	// every forex fill. Held under KindForexBook so menjačnica's
	// system-account lookups don't ever pick it up.
	const forexBookFloat = "1000000000.0000"
	for _, c := range domain.SupportedCurrencies() {
		if _, err := s.Store.GetForexBookAccount(ctx, c); err == nil {
			continue
		} else if !isNotFound(err) {
			return err
		}
		number, err := account.Generate(s.Cfg.BankCode, s.Cfg.Branch, account.TypeSystem)
		if err != nil {
			return err
		}
		_, err = s.Store.CreateAccount(ctx, &domain.Account{
			Number:              number,
			Name:                "Forex book (" + string(c) + ")",
			OwnerClientID:       domain.ForexBookOwnerID,
			CreatedByEmployeeID: domain.SystemOwnerID,
			Kind:                domain.KindForexBook,
			Subtype:             domain.SubtypeUnspecified,
			Currency:            c,
			Status:              domain.AccountActive,
			Balance:             forexBookFloat,
			AvailableBalance:    forexBookFloat,
			MaintenanceFee:      "0",
			DailyLimit:          "0",
			MonthlyLimit:        "0",
			DailySpent:          "0",
			MonthlySpent:        "0",
		})
		if err != nil {
			return err
		}
		s.Log.Info("seeded forex book account", "currency", c, "number", number)
	}
	return nil
}

// =====================================================================
// helpers
// =====================================================================

func canSeeAccount(p auth.Principal, a *domain.Account) bool {
	if p.UserKind == auth.KindClient {
		return a.OwnerClientID == p.UserID
	}
	return permissions.HasAny(p.Permissions, permissions.AccountRead, permissions.Admin)
}

// kindAndSubtypeToAccountType picks the trailing two-digit account-type
// code per spec p.16. Personal RSD checking branches on subtype because
// the spec gives savings/pensioner/youth/student/unemployed each their
// own code; business RSD checking collapses DOO/AD/Fondacija onto 12;
// the FX bucket has no subtypes (21 lični, 22 poslovni).
func kindAndSubtypeToAccountType(k domain.AccountKind, st domain.AccountSubtype) account.Type {
	switch k {
	case domain.KindPersonalCheckingRSD:
		switch st {
		case domain.SubtypeSavings:
			return account.TypeSavings
		case domain.SubtypePensioner:
			return account.TypePensioner
		case domain.SubtypeYouth:
			return account.TypeYouth
		case domain.SubtypeStudent:
			return account.TypeStudent
		case domain.SubtypeUnemployed:
			return account.TypeUnemployed
		}
		// SubtypeStandard (and any unset) → 11.
		return account.TypePersonalChecking
	case domain.KindBusinessCheckingRSD:
		return account.TypeBusinessChecking
	case domain.KindPersonalFX:
		return account.TypePersonalFX
	case domain.KindBusinessFX:
		return account.TypeBusinessFX
	case domain.KindSystem:
		return account.TypeSystem
	}
	return account.TypePersonalChecking
}

func defaultAccountName(k domain.AccountKind, c domain.Currency) string {
	switch k {
	case domain.KindPersonalCheckingRSD, domain.KindBusinessCheckingRSD:
		return "Tekući račun (" + string(c) + ")"
	case domain.KindPersonalFX, domain.KindBusinessFX:
		return "Devizni račun (" + string(c) + ")"
	}
	return "Račun"
}

func (s *Service) validateCreateAccount(in CreateAccountInput) error {
	if strings.TrimSpace(in.OwnerClientID) == "" {
		return apperr.Validation("owner_client_id is required")
	}
	if !in.Currency.Supported() {
		return apperr.Validation("unsupported currency")
	}

	switch in.Kind {
	case domain.KindPersonalCheckingRSD:
		if in.Currency != domain.CurrencyRSD {
			return apperr.Validation("personal checking accounts are RSD only")
		}
		if !isPersonalSubtype(in.Subtype) {
			return apperr.Validation("personal checking accounts require a personal subtype (standard, savings, ...)")
		}
		if in.CompanyID != "" {
			return apperr.Validation("personal accounts must not have a company")
		}
	case domain.KindPersonalFX:
		if in.Currency == domain.CurrencyRSD {
			return apperr.Validation("FX account currency cannot be RSD")
		}
		if in.CompanyID != "" {
			return apperr.Validation("personal accounts must not have a company")
		}
		// Subtype not used here; force unspecified.
		if in.Subtype != "" && in.Subtype != domain.SubtypeUnspecified {
			return apperr.Validation("personal FX accounts must not declare a subtype")
		}
	case domain.KindBusinessCheckingRSD:
		if in.Currency != domain.CurrencyRSD {
			return apperr.Validation("business checking accounts are RSD only")
		}
		if !isBusinessSubtype(in.Subtype) {
			return apperr.Validation("business accounts require a business subtype (doo, ad, foundation)")
		}
		if strings.TrimSpace(in.CompanyID) == "" {
			return apperr.Validation("business accounts require a company id")
		}
	case domain.KindBusinessFX:
		if in.Currency == domain.CurrencyRSD {
			return apperr.Validation("FX account currency cannot be RSD")
		}
		if strings.TrimSpace(in.CompanyID) == "" {
			return apperr.Validation("business accounts require a company id")
		}
	default:
		return apperr.Validation("unsupported account kind for client creation (system accounts are seeded internally)")
	}
	return nil
}

func isPersonalSubtype(s domain.AccountSubtype) bool {
	switch s {
	case domain.SubtypeStandard, domain.SubtypeSavings, domain.SubtypePensioner,
		domain.SubtypeYouth, domain.SubtypeStudent, domain.SubtypeUnemployed:
		return true
	}
	return false
}

func isBusinessSubtype(s domain.AccountSubtype) bool {
	switch s {
	case domain.SubtypeDOO, domain.SubtypeAD, domain.SubtypeFoundation:
		return true
	}
	return false
}

func isNotFound(err error) bool {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Kind == apperr.KindNotFound
}
