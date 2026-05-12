// Package domain holds the trading service's value types. No I/O.
//
// All money/quantity columns are decimal strings; the service layer
// uses pkg/money.Parse to do exact arithmetic and pkg/money.FormatAmount
// to render output. Wire types in the proto layer mirror the strings
// 1:1.
package domain

import "time"

// Currency mirrors the proto Currency enum tail; values match the DB
// check constraints in trading.* and bank.* schemas.
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

// Supported reports whether c is one of the system's supported currencies.
func (c Currency) Supported() bool {
	switch c {
	case CurrencyRSD, CurrencyEUR, CurrencyCHF, CurrencyUSD,
		CurrencyGBP, CurrencyJPY, CurrencyCAD, CurrencyAUD:
		return true
	}
	return false
}

// UserKind discriminates an order/holding owner. Matches the auth
// pkg's UserKind values.
type UserKind string

const (
	KindClient   UserKind = "client"
	KindEmployee UserKind = "employee"
)

// ActuaryType maps to the proto enum + DB check constraint.
type ActuaryType string

const (
	ActuarySupervisor ActuaryType = "supervisor"
	ActuaryAgent      ActuaryType = "agent"
)

// SecurityType.
type SecurityType string

const (
	SecurityStock  SecurityType = "stock"
	SecurityFuture SecurityType = "future"
	SecurityForex  SecurityType = "forex"
	SecurityOption SecurityType = "option"
)

// OrderType.
type OrderType string

const (
	OrderMarket    OrderType = "market"
	OrderLimit     OrderType = "limit"
	OrderStop      OrderType = "stop"
	OrderStopLimit OrderType = "stop_limit"
)

// Direction.
type Direction string

const (
	DirectionBuy  Direction = "buy"
	DirectionSell Direction = "sell"
)

// OrderStatus.
type OrderStatus string

const (
	OrderStatusPending  OrderStatus = "pending"
	OrderStatusApproved OrderStatus = "approved"
	OrderStatusDeclined OrderStatus = "declined"
)

// OptionType.
type OptionType string

const (
	OptionCall OptionType = "call"
	OptionPut  OptionType = "put"
)

// =====================================================================
// Entities
// =====================================================================

// ActuaryInfo extends user.users (employee) with trading-specific
// state. Populated only for employees who hold actuary.* permissions.
// daily_limit / used_limit are stored in RSD; per spec p.38 the
// limit applies to the running RSD-equivalent of all approved trades.
type ActuaryInfo struct {
	EmployeeID   string
	Type         ActuaryType
	DailyLimit   string
	UsedLimit    string
	NeedApproval bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ExchangeOverrideState forces the resolver onto one of three modes
// regardless of wall-clock. Spec p.39 only requires open/closed; the
// after_hours value exists so admins can drive the spec p.56
// after-hours cadence path at any time of day.
type ExchangeOverrideState string

const (
	ExchangeOverrideOpen       ExchangeOverrideState = "open"
	ExchangeOverrideClosed     ExchangeOverrideState = "closed"
	ExchangeOverrideAfterHours ExchangeOverrideState = "after_hours"
)

// Exchange is one venue (NYSE, NASDAQ, …). override_state is a four-
// state text column: nil → follow schedule, "open" → forced open,
// "closed" → forced closed, "after_hours" → forced after-hours
// (closed but within the 4h window). The "is_open" / "is_after_hours"
// flags exposed at the proto boundary are computed from these fields
// plus the wall clock.
type Exchange struct {
	MIC           string
	Name          string
	Acronym       string
	Polity        string
	Currency      Currency
	Timezone      string
	OpenLocal     string // "HH:MM"
	CloseLocal    string
	OverrideState *ExchangeOverrideState
	UpdatedAt     time.Time
}

// Security is the polymorphic instrument type (stock / future / forex
// / option). Per-type fields are populated only when relevant.
type Security struct {
	ID                   string
	Ticker               string
	Name                 string
	Type                 SecurityType
	ExchangeMIC          string
	Currency             Currency

	// Stock
	OutstandingShares int64
	DividendYield     string

	// Future / forex
	ContractSize   string
	ContractUnit   string
	SettlementDate *time.Time

	// Forex
	BaseCurrency  Currency
	QuoteCurrency Currency
	Liquidity     string

	// Option
	UnderlyingSecurityID string
	OptionType           OptionType
	StrikePrice          string
	ImpliedVolatility    string
	Premium              string
	OpenInterest         int64

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Listing is the live-price snapshot per security (and optionally
// exchange). Spec p.45 entity.
type Listing struct {
	ID           string
	SecurityID   string
	ExchangeMIC  string
	Price        string
	Ask          string
	Bid          string
	Volume       int64
	ChangeAmt    string
	ContractSize string
	LastRefresh  time.Time
	CreatedAt    time.Time
}

// ListingDailyPrice is one historical row per spec p.45.
type ListingDailyPrice struct {
	ListingID string
	Date      time.Time
	Price     string
	Ask       string
	Bid       string
	ChangeAmt string
	Volume    int64
}

// Order is one submitted nalog. Spec p.49.
//
// IsActuary is captured at create-time from the principal's permissions
// rather than re-derived from UserKind. Frozen on the row so a future
// "trading.client + employee" combo doesn't misroute through the FX
// commission-zero path on settle (spec p.26 / p.55-56).
type Order struct {
	ID                 string
	UserID             string
	UserKind           UserKind
	SecurityID         string
	OrderType          OrderType
	Direction          Direction
	Quantity           int32
	ContractSize       string
	PricePerUnit       string
	LimitPrice         string
	StopPrice          string
	AllOrNone          bool
	Margin             bool
	IsActuary          bool
	AccountID          string
	Status             OrderStatus
	ApprovedBy         string
	ApprovalRequired   bool
	ApprovedAt         *time.Time
	IsDone             bool
	Cancelled          bool
	Triggered          bool
	AfterHours         bool
	RemainingQuantity  int32
	LastModification   time.Time
	CreatedAt          time.Time
}

// OrderExecution is one partial fill. Spec p.55-56.
//
// Status walks `pending -> settled` in the executeFill saga (BE-3): a
// fill is inserted as `pending` before the bank call so a worker crash
// between settle and book leaves a durable signal to resume.
type OrderExecution struct {
	ID            string
	OrderID       string
	Quantity      int32
	PricePerUnit  string
	TotalAmount   string
	CommissionAmt string
	BankOpID      string
	Status        string
	ExecutedAt    time.Time
}

// Holding is one row in a user's portfolio.
//
// public_count and reserved_count are orthogonal (spec p.68):
//   - PublicCount is visibility on the OTC discovery board.
//   - ReservedCount is commitment behind in-flight OTC offers + signed
//     active contracts. Discovery surfaces PublicCount − ReservedCount
//     as the "available now" number; the schema CHECK guarantees
//     reserved_count ≤ quantity.
type Holding struct {
	ID               string
	UserID           string
	UserKind         UserKind
	SecurityID       string
	AccountID        string
	Quantity         int32
	WeightedAvgPrice string
	PublicCount      int32
	ReservedCount    int32
	AcquiredAt       time.Time
	UpdatedAt        time.Time
}

// OptionExercise records an actuary's option-exercise event. Two-phase
// like OrderExecution (pending → settled): the pending UUID is the
// op_id for the bank settle, so a retry after a partial failure is
// idempotent at the bank layer.
type OptionExercise struct {
	ID                   string
	OptionHoldingID      string
	UserID               string
	UserKind             UserKind
	OptionSecurityID     string
	UnderlyingSecurityID string
	AccountID            string
	OptionType           OptionType
	Quantity             int32
	ContractSize         string
	StrikePrice          string
	NotionalAmt          string
	Currency             Currency
	BankOpID             string
	RealizedGainID       string
	Status               string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// =====================================================================
// OTC (c4 — spec p.64-69, 79)
// =====================================================================

// OTCStatus is the lifecycle state of a single offer iteration.
//
//   - open       → the live row in a thread; counterparty's turn to act.
//   - superseded → a prior iteration in the same thread; kept for audit.
//   - accepted   → the iteration the counterparty accepted; promoted to
//     an otc_contracts row.
//   - withdrawn  → either party pulled out before accept.
//   - expired    → never used today (offers don't TTL); reserved for
//     future "auto-withdraw stale threads" policy.
type OTCStatus string

const (
	OTCStatusOpen       OTCStatus = "open"
	OTCStatusSuperseded OTCStatus = "superseded"
	OTCStatusAccepted   OTCStatus = "accepted"
	OTCStatusWithdrawn  OTCStatus = "withdrawn"
	OTCStatusExpired    OTCStatus = "expired"
)

// OTCContractStatus is the lifecycle of a signed option contract.
//
//   - active     → premium settled, exercisable until settlement_date.
//   - exercised  → buyer exercised; underlying + cash legs settled.
//   - expired    → past settlement_date without exercise; premium sunk.
//   - settling   → exercise saga in flight (reserved for future use; the
//     exercise saga today flips the row straight to `exercised` on the
//     finalize step, but the state exists so an extended exercise flow
//     can mark the row mid-saga).
type OTCContractStatus string

const (
	OTCContractActive    OTCContractStatus = "active"
	OTCContractExercised OTCContractStatus = "exercised"
	OTCContractExpired   OTCContractStatus = "expired"
	OTCContractSettling  OTCContractStatus = "settling"
)

// OTCOffer is one iteration in a negotiation thread. Spec p.67-69.
//
// thread_id groups iterations; the first iteration's id is reused as
// the thread_id by convention (the store enforces this).
type OTCOffer struct {
	ID              string
	ThreadID        string
	SecurityID      string
	SellerHoldingID string

	BuyerID        string
	BuyerKind      UserKind
	BuyerAccountID string

	SellerID        string
	SellerKind      UserKind
	SellerAccountID string

	Quantity       int32
	PricePerUnit   string
	Premium        string
	Currency       Currency
	SettlementDate time.Time

	ModifiedBy string
	Status     OTCStatus

	CreatedAt time.Time
	UpdatedAt time.Time
}

// OTCContract is the signed option contract minted on accept.
// Spec p.67.b: strike = price_per_unit, premium paid up-front,
// exercise window until settlement_date.
type OTCContract struct {
	ID              string
	ThreadID        string
	SecurityID      string
	SellerHoldingID string

	BuyerID        string
	BuyerKind      UserKind
	BuyerAccountID string

	SellerID        string
	SellerKind      UserKind
	SellerAccountID string

	Quantity       int32
	StrikePrice    string
	PremiumPaid    string
	Currency       Currency
	SettlementDate time.Time

	PremiumOpID    string
	Status         OTCContractStatus
	ExercisedOpID  string
	ExerciseSagaID string
	ExercisedAt    *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// RealizedGain records one closing sell-execution for capital-gains
// tax. Spec p.62.
type RealizedGain struct {
	ID            string
	UserID        string
	UserKind      UserKind
	SecurityID    string
	AccountID     string
	Quantity      int32
	CostBasisAmt  string
	ProceedsAmt   string
	Currency      Currency
	GainNative    string
	GainRSD       string
	RealizedAt    time.Time
	Taxed         bool
	TaxedAt       *time.Time
	TaxOpID       string
}
