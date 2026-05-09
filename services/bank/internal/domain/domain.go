// Package domain holds the bank service's value types. No I/O.
package domain

import "time"

// SystemOwnerID is the sentinel UUID used as owner_client_id for bank-
// owned (system) accounts. Hardcoded as the nil UUID so it can never
// collide with a real Klijent ID. Cross-schema joins are forbidden by
// convention; readers strip rows where owner == this.
const SystemOwnerID = "00000000-0000-0000-0000-000000000000"

// StateTaxOwnerID is the sentinel UUID used as owner_client_id for the
// state's capital-gains tax RSD account (spec p.62). The end-of-month
// tax cron credits this account; we keep it distinct from the
// menjačnica house accounts so neither path bleeds into the other.
const StateTaxOwnerID = "00000000-0000-0000-0000-000000000010"

// =====================================================================
// Currency
// =====================================================================

type Currency string

const (
	CurrencyRSD Currency = "RSD"
	CurrencyEUR Currency = "EUR"
	CurrencyCHF Currency = "CHF"
	CurrencyUSD Currency = "USD"
	CurrencyGBP Currency = "GBP"
	CurrencyJPY Currency = "JPY"
	CurrencyCAD Currency = "CAD"
	CurrencyAUD Currency = "AUD"
)

func (c Currency) Supported() bool {
	switch c {
	case CurrencyRSD, CurrencyEUR, CurrencyCHF, CurrencyUSD,
		CurrencyGBP, CurrencyJPY, CurrencyCAD, CurrencyAUD:
		return true
	}
	return false
}

// SupportedCurrencies returns the canonical iteration order.
func SupportedCurrencies() []Currency {
	return []Currency{
		CurrencyRSD, CurrencyEUR, CurrencyCHF, CurrencyUSD,
		CurrencyGBP, CurrencyJPY, CurrencyCAD, CurrencyAUD,
	}
}

// =====================================================================
// Account kind / subtype / status
// =====================================================================

type AccountKind string

const (
	KindPersonalCheckingRSD AccountKind = "personal_checking_rsd"
	KindPersonalFX          AccountKind = "personal_fx"
	KindBusinessCheckingRSD AccountKind = "business_checking_rsd"
	KindBusinessFX          AccountKind = "business_fx"
	KindSystem              AccountKind = "system"
	KindStateTax            AccountKind = "state_tax"
)

func (k AccountKind) IsBusiness() bool {
	return k == KindBusinessCheckingRSD || k == KindBusinessFX
}

func (k AccountKind) IsPersonal() bool {
	return k == KindPersonalCheckingRSD || k == KindPersonalFX
}

type AccountSubtype string

const (
	SubtypeUnspecified AccountSubtype = "unspecified"
	SubtypeStandard    AccountSubtype = "standard"
	SubtypeSavings     AccountSubtype = "savings"
	SubtypePensioner   AccountSubtype = "pensioner"
	SubtypeYouth       AccountSubtype = "youth"
	SubtypeStudent     AccountSubtype = "student"
	SubtypeUnemployed  AccountSubtype = "unemployed"
	SubtypeDOO         AccountSubtype = "doo"
	SubtypeAD          AccountSubtype = "ad"
	SubtypeFoundation  AccountSubtype = "foundation"
)

type AccountStatus string

const (
	AccountActive   AccountStatus = "active"
	AccountInactive AccountStatus = "inactive"
)

// =====================================================================
// Entities
// =====================================================================

type Company struct {
	ID            string
	Name          string
	RegistryID    string
	TaxID         string
	ActivityCode  string
	Address       string
	OwnerClientID string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Account mirrors spec p.12-13 fields. Money is decimal-string in/out;
// pgx scans the numeric column into a string via the ::text cast in
// the SELECT lists.
type Account struct {
	ID                    string
	Number                string
	Name                  string
	OwnerClientID         string
	CompanyID             string // empty for personal
	CreatedByEmployeeID   string
	Kind                  AccountKind
	Subtype               AccountSubtype
	Currency              Currency
	Status                AccountStatus
	Balance               string
	AvailableBalance      string
	MaintenanceFee        string
	DailyLimit            string
	MonthlyLimit          string
	DailySpent            string
	MonthlySpent          string
	CreatedAt             time.Time
	ExpiresAt             *time.Time
	UpdatedAt             time.Time
	LastMaintenanceDebit  *time.Time // null until the first monthly cron run
}

// =====================================================================
// Filters
// =====================================================================

type CompanyFilter struct {
	Name       string
	RegistryID string
}

type AccountFilter struct {
	OwnerClientID string
	Kind          AccountKind
	Currency      Currency
	Status        AccountStatus
}
