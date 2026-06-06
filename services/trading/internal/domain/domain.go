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
	// KindFund identifies investment-fund-as-actor rows (spec
	// p.74-75). A fund-actor order books holdings to
	// (user_id=fund.id, user_kind='fund') and skips realized_gains
	// (funds are pre-tax; the client pays at withdrawal).
	KindFund UserKind = "fund"
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
	ID          string
	Ticker      string
	Name        string
	Type        SecurityType
	ExchangeMIC string
	Currency    Currency

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
	ID                string
	UserID            string
	UserKind          UserKind
	SecurityID        string
	OrderType         OrderType
	Direction         Direction
	Quantity          int32
	ContractSize      string
	PricePerUnit      string
	LimitPrice        string
	StopPrice         string
	AllOrNone         bool
	Margin            bool
	IsActuary         bool
	AccountID         string
	Status            OrderStatus
	ApprovedBy        string
	ApprovalRequired  bool
	ApprovedAt        *time.Time
	IsDone            bool
	Cancelled         bool
	Triggered         bool
	AfterHours        bool
	RemainingQuantity int32
	LastModification  time.Time
	CreatedAt         time.Time

	// Fund-actor (spec p.74-75). ActorKind discriminates whether the
	// order was placed by a client/employee themselves or on behalf of
	// an investment fund they manage. OnBehalfOfFundID is non-empty when
	// ActorKind == KindFund; otherwise the column defaults to
	// KindClient/KindEmployee matching UserKind.
	ActorKind        UserKind
	OnBehalfOfFundID string

	// Per-order execution aggregates over settled order_executions
	// (todoSpec S30/S31). Populated by the order read queries via a
	// LEFT JOIN subquery; all three are zero-valued on an order with no
	// settled fills. AvgExecutionPrice is the quantity-weighted average
	// fill price; TotalCommission is the sum of commission_amt;
	// LastExecutionAt is the most recent settled fill time (nil = none).
	AvgExecutionPrice string
	TotalCommission   string
	LastExecutionAt   *time.Time
}

// OrderExecution is one partial fill. Spec p.55-56.
//
// Status walks `pending -> settled` in the executeFill saga: a fill is
// inserted as `pending` before the bank call so a worker crash between
// settle and book leaves a durable signal to resume.
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

// =====================================================================
// External OTC (celina 5 — spec p.77+)
// =====================================================================
//
// Mirrors the local OTC types above (OTCOffer / OTCContract) but for
// threads where one party lives at a remote bank. Reached via the
// gateway's partner-REST adapter; cross-bank cash legs go through
// bank's 2PC primitive.

// ExternalOTCDirection encodes who initiated a thread.
type ExternalOTCDirection string

const (
	// ExternalOTCOutgoing — local user is the buyer; partner is the seller.
	ExternalOTCOutgoing ExternalOTCDirection = "outgoing"
	// ExternalOTCIncoming — partner is the buyer; local user is the seller.
	ExternalOTCIncoming ExternalOTCDirection = "incoming"
)

// ExternalOTCSide marks which side of the thread an iteration came from.
type ExternalOTCSide string

const (
	ExternalOTCSideLocal  ExternalOTCSide = "local"
	ExternalOTCSideRemote ExternalOTCSide = "remote"
)

// ExternalOTCRole captures the local user's role in the thread.
type ExternalOTCRole string

const (
	ExternalOTCRoleBuyer  ExternalOTCRole = "buyer"
	ExternalOTCRoleSeller ExternalOTCRole = "seller"
)

// ExternalOTCThreadStatus mirrors OTCStatus + adds `rejected` for the
// case where a partner returns a terminal error during a write.
type ExternalOTCThreadStatus string

const (
	ExternalOTCThreadOpen       ExternalOTCThreadStatus = "open"
	ExternalOTCThreadSuperseded ExternalOTCThreadStatus = "superseded"
	ExternalOTCThreadAccepted   ExternalOTCThreadStatus = "accepted"
	ExternalOTCThreadWithdrawn  ExternalOTCThreadStatus = "withdrawn"
	ExternalOTCThreadExpired    ExternalOTCThreadStatus = "expired"
	ExternalOTCThreadRejected   ExternalOTCThreadStatus = "rejected"
)

// ExternalOTCContractStatus mirrors OTCContractStatus.
type ExternalOTCContractStatus string

const (
	ExternalOTCContractActive    ExternalOTCContractStatus = "active"
	ExternalOTCContractExercised ExternalOTCContractStatus = "exercised"
	ExternalOTCContractExpired   ExternalOTCContractStatus = "expired"
	ExternalOTCContractSettling  ExternalOTCContractStatus = "settling"
)

// ExternalOTCThread is the cross-bank counterpart of OTCOffer's
// thread view: one row per active negotiation. Local-side fields are
// uuid; partner-side fields stay as text since not every partner
// uses UUIDs.
type ExternalOTCThread struct {
	ID        string
	Direction ExternalOTCDirection

	RemoteBankCode    string
	RemoteThreadID    string
	RemoteUserRef     string
	RemoteDisplayName string
	RemoteAccountRef  string

	LocalUserID        string
	LocalUserKind      UserKind
	LocalAccountID     string
	LocalAccountNumber string
	LocalRole          ExternalOTCRole

	SecurityID       string // may be empty for outgoing threads not yet mirrored locally
	SecurityTicker   string
	SellerHoldingRef string

	Quantity       int32
	PricePerUnit   string
	Premium        string
	Currency       Currency
	SettlementDate time.Time

	ModifiedBySide ExternalOTCSide
	Status         ExternalOTCThreadStatus

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ExternalOTCIteration is one row of the negotiation audit log.
type ExternalOTCIteration struct {
	ID             string
	ThreadID       string
	ProposedBySide ExternalOTCSide
	Quantity       int32
	PricePerUnit   string
	Premium        string
	SettlementDate time.Time
	CreatedAt      time.Time
}

// ExternalOTCContract is the option contract minted on accept.
type ExternalOTCContract struct {
	ID        string
	ThreadID  string
	Direction ExternalOTCDirection

	RemoteBankCode    string
	RemoteThreadID    string
	RemoteUserRef     string
	RemoteDisplayName string
	RemoteAccountRef  string

	LocalUserID        string
	LocalUserKind      UserKind
	LocalAccountID     string
	LocalAccountNumber string
	LocalRole          ExternalOTCRole

	SecurityID       string
	SecurityTicker   string
	SellerHoldingRef string

	Quantity       int32
	StrikePrice    string
	PremiumPaid    string
	Currency       Currency
	SettlementDate time.Time

	AcceptedBySide ExternalOTCSide
	Status         ExternalOTCContractStatus

	PremiumOpID  string
	ExerciseOpID string
	ExercisedAt  *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// =====================================================================
// Investment funds (spec p.71-76)
// =====================================================================

// FundStatus is the lifecycle of an investment fund.
type FundStatus string

const (
	FundActive FundStatus = "active"
	FundClosed FundStatus = "closed"
)

// FundTransactionStatus is the lifecycle of an invest/withdraw row.
//
//   - pending   → SAGA is in flight (illiquid withdraw stays here while
//     auto-liquidation orders settle).
//   - completed → terminal success.
//   - failed    → terminal failure (compensations ran).
type FundTransactionStatus string

const (
	FundTxPending   FundTransactionStatus = "pending"
	FundTxCompleted FundTransactionStatus = "completed"
	FundTxFailed    FundTransactionStatus = "failed"
)

// Fund is one investment-fund row. total_value_rsd + profit_rsd are
// derived at read time from the bank account balance (liquid) plus the
// market value of the fund's holdings (Σ qty × current_price, all in
// RSD via the rate provider's ASK).
type Fund struct {
	ID                  string
	Name                string
	Description         string
	ManagerUserID       string
	BankAccountID       string
	MinimumContribution string
	TotalUnits          string
	Status              FundStatus
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// FundPosition is one client's stake in one fund. units is the
// mutual-fund unit count; total_invested_rsd is the cash that bought
// those units (cost basis for the tax row at withdrawal time).
type FundPosition struct {
	ID               string
	FundID           string
	ClientID         string
	Units            string
	TotalInvestedRSD string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// FundTransaction is one audit-log row for an invest/withdraw.
type FundTransaction struct {
	ID                    string
	FundID                string
	ClientID              string
	InitiatorEmployeeID   string
	AmountRSD             string
	UnitsDelta            string // positive on invest, negative on withdraw
	SourceOrDestAccountID string
	IsInflow              bool
	Status                FundTransactionStatus
	SagaID                string
	FailureReason         string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// FundPerformanceSnapshot is one daily snapshot for the chart.
type FundPerformanceSnapshot struct {
	FundID           string
	SnapshotAt       time.Time
	LiquidRSD        string
	HoldingsValueRSD string
}

// RealizedGain records one closing sell-execution for capital-gains
// tax. Spec p.62. SecurityID is empty on fund-withdrawal rows;
// FundID is non-empty there instead. The tax cron is
// agnostic — it groups by (user_id, account_id) and doesn't read
// security_id.
type RealizedGain struct {
	ID           string
	UserID       string
	UserKind     UserKind
	SecurityID   string
	FundID       string
	AccountID    string
	Quantity     int32
	CostBasisAmt string
	ProceedsAmt  string
	Currency     Currency
	GainNative   string
	GainRSD      string
	RealizedAt   time.Time
	Taxed        bool
	TaxedAt      *time.Time
	TaxOpID      string
}

// DividendPayout records one quarterly dividend credited to a holder
// (todoSpec C3 S54-S59). GrossAmount + Currency are the dividend in the
// security's listing currency; AccountID is the bank account actually
// credited (purchase account → default-currency account → RSD account,
// S54/S55/S56). TaxRSD is the 15% capital-gains tax owed for client
// holders (S57); "0" for actuary "in the name of the bank" holdings,
// which route to Profit Banke untaxed (S58).
type DividendPayout struct {
	ID          string
	UserID      string
	UserKind    UserKind
	SecurityID  string
	Quantity    int32
	Price       string
	GrossAmount string
	Currency    Currency
	AccountID   string
	TaxRSD      string
	OpID        string
	Status      string
	PaidAt      *time.Time
	CreatedAt   time.Time
}

// PriceAlertCondition discriminates the threshold-crossing direction
// (todoSpec C3 S26-S29). Values match the price_alerts.condition check
// constraint.
type PriceAlertCondition string

const (
	// PriceAlertAbove fires when the security's current price rises to or
	// above the threshold (price >= threshold).
	PriceAlertAbove PriceAlertCondition = "ABOVE"
	// PriceAlertBelow fires when the security's current price falls to or
	// below the threshold (price <= threshold).
	PriceAlertBelow PriceAlertCondition = "BELOW"
)

// Valid reports whether c is a recognised condition.
func (c PriceAlertCondition) Valid() bool {
	return c == PriceAlertAbove || c == PriceAlertBelow
}

// PriceAlert is a one-shot price-threshold notification on a security.
// When the sweep observes the crossing it sends one notification and
// flips IsActive to false (TriggeredAt stamped). Threshold is a decimal
// string in the security's listing currency.
type PriceAlert struct {
	ID          string
	UserID      string
	UserKind    UserKind
	SecurityID  string
	Threshold   string
	Condition   PriceAlertCondition
	IsActive    bool
	CreatedAt   time.Time
	TriggeredAt *time.Time
}

// RecurringMode discriminates how a recurring order ("Trajni nalog" /
// DCA — todoSpec C3 S47-S53) sizes each scheduled BUY. Values match the
// recurring_orders.mode check constraint.
type RecurringMode string

const (
	// RecurringByAmount sizes each BUY so its RSD notional matches
	// AmountRSD (S47); the cron derives the share quantity from the
	// security's current RSD-equivalent price at execution time.
	RecurringByAmount RecurringMode = "BYAMOUNT"
	// RecurringByQuantity buys a fixed share Quantity each cycle (S48).
	RecurringByQuantity RecurringMode = "BYQUANTITY"
)

// Valid reports whether m is a recognised recurring mode.
func (m RecurringMode) Valid() bool {
	return m == RecurringByAmount || m == RecurringByQuantity
}

// RecurringOrder is a scheduled, repeating Market BUY of a security
// ("Trajni nalog" / dollar-cost-averaging — todoSpec C3 S47-S53). On
// each NextRun a cron creates one Market BUY order (for AmountRSD worth
// of shares, or a fixed Quantity) and advances NextRun by the Cadence.
// Pausing sets Active=false; cancelling deactivates the row permanently.
// Direction is always 'BUY' in the current scope (DCA accumulation).
type RecurringOrder struct {
	ID         string
	UserID     string
	UserKind   UserKind
	SecurityID string
	Direction  Direction
	Mode       RecurringMode
	// AmountRSD is the per-cycle RSD notional for BYAMOUNT orders;
	// empty for BYQUANTITY.
	AmountRSD string
	// Quantity is the per-cycle share count for BYQUANTITY orders;
	// 0 for BYAMOUNT.
	Quantity  int32
	AccountID string
	Cadence   string
	NextRun   time.Time
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Watchlist is a user-owned, named collection of securities the user
// wants to keep an eye on (todoSpec C3 S35-S39). A user can have many
// (S36). Items hang off the list and cascade-delete with it.
type Watchlist struct {
	ID        string
	UserID    string
	UserKind  UserKind
	Name      string
	CreatedAt time.Time
	Items     []*WatchlistItem
}

// WatchlistItem is one security on a watchlist. The decorated fields
// (Ticker, Name, SecurityType, Price, DailyChange, Currency) are
// joined in at the service layer from the security + its listing so the
// FE can render the header row (current price + daily change, S35) and
// filter by security type (S39) without extra round-trips. They are
// empty when no listing/security row resolves.
type WatchlistItem struct {
	ID         string
	SecurityID string
	CreatedAt  time.Time

	// Decorated (not persisted on watchlist_items).
	Ticker       string
	Name         string
	SecurityType SecurityType
	Currency     Currency
	Price        string
	DailyChange  string
}
