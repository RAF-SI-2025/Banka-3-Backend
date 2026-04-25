package trading

import "time"

type ForexLiquidity string

const (
	LiquidityHigh   ForexLiquidity = "high"
	LiquidityMedium ForexLiquidity = "medium"
	LiquidityLow    ForexLiquidity = "low"
)

type OptionType string

const (
	OptionCall OptionType = "call"
	OptionPut  OptionType = "put"
)

type OrderType string

const (
	OrderMarket    OrderType = "market"
	OrderLimit     OrderType = "limit"
	OrderStop      OrderType = "stop"
	OrderStopLimit OrderType = "stop_limit"
)

type OrderDirection string

const (
	DirectionBuy  OrderDirection = "buy"
	DirectionSell OrderDirection = "sell"
)

type OrderStatus string

const (
	StatusPending   OrderStatus = "pending"
	StatusApproved  OrderStatus = "approved"
	StatusDeclined  OrderStatus = "declined"
	StatusDone      OrderStatus = "done"
	StatusCancelled OrderStatus = "cancelled"
)

type Exchange struct {
	ID             int64  `gorm:"column:id;primaryKey"`
	Name           string `gorm:"column:name;type:varchar(127);not null"`
	Acronym        string `gorm:"column:acronym;type:varchar(31);not null"`
	MICCode        string `gorm:"column:mic_code;type:varchar(8);not null;unique"`
	Polity         string `gorm:"column:polity;type:varchar(127);not null"`
	Currency       string `gorm:"column:currency;type:varchar(8);not null"`
	TimeZoneOffset string `gorm:"column:time_zone_offset;type:varchar(8);not null"`
	// Local-time working hours. Stored as Postgres TIME; surfaced as "HH:MM:SS"
	// strings so that IsOpen can parse them directly without dragging in a
	// gorm-specific time-of-day type.
	OpenTime     string `gorm:"column:open_time;type:time;not null"`
	CloseTime    string `gorm:"column:close_time;type:time;not null"`
	OpenOverride bool   `gorm:"column:open_override;not null;default:false"`
}

func (Exchange) TableName() string { return "exchanges" }

type Stock struct {
	ID                int64   `gorm:"column:id;primaryKey"`
	Ticker            string  `gorm:"column:ticker;type:varchar(8);not null;unique"`
	Name              string  `gorm:"column:name;type:varchar(127);not null"`
	OutstandingShares int64   `gorm:"column:outstanding_shares;not null;default:0"`
	DividendYield     float64 `gorm:"column:dividend_yield;type:decimal(10,6);not null;default:0"`
}

func (Stock) TableName() string { return "stocks" }

type Future struct {
	ID             int64     `gorm:"column:id;primaryKey"`
	Ticker         string    `gorm:"column:ticker;type:varchar(16);not null;unique"`
	Name           string    `gorm:"column:name;type:varchar(127);not null"`
	ContractSize   int64     `gorm:"column:contract_size;not null"`
	ContractUnit   string    `gorm:"column:contract_unit;type:varchar(31);not null"`
	SettlementDate time.Time `gorm:"column:settlement_date;type:date;not null"`
}

func (Future) TableName() string { return "futures" }

type ForexPair struct {
	ID            int64          `gorm:"column:id;primaryKey"`
	Ticker        string         `gorm:"column:ticker;type:varchar(8);not null;unique"`
	Name          string         `gorm:"column:name;type:varchar(127);not null"`
	BaseCurrency  string         `gorm:"column:base_currency;type:varchar(8);not null"`
	QuoteCurrency string         `gorm:"column:quote_currency;type:varchar(8);not null"`
	ExchangeRate  float64        `gorm:"column:exchange_rate;type:decimal(20,6);not null"`
	Liquidity     ForexLiquidity `gorm:"column:liquidity;type:forex_liquidity;not null;default:'medium'"`
}

func (ForexPair) TableName() string { return "forex_pairs" }

type Option struct {
	ID                int64      `gorm:"column:id;primaryKey"`
	Ticker            string     `gorm:"column:ticker;type:varchar(32);not null;unique"`
	Name              string     `gorm:"column:name;type:varchar(127);not null"`
	StockID           int64      `gorm:"column:stock_id;not null"`
	OptionType        OptionType `gorm:"column:option_type;type:option_type;not null"`
	StrikePrice       int64      `gorm:"column:strike_price;not null"`
	Premium           int64      `gorm:"column:premium;not null"`
	ImpliedVolatility float64    `gorm:"column:implied_volatility;type:decimal(10,4);not null;default:0"`
	OpenInterest      int64      `gorm:"column:open_interest;not null;default:0"`
	SettlementDate    time.Time  `gorm:"column:settlement_date;type:date;not null"`
}

func (Option) TableName() string { return "options" }

type Listing struct {
	ID          int64     `gorm:"column:id;primaryKey"`
	ExchangeID  int64     `gorm:"column:exchange_id;not null"`
	StockID     *int64    `gorm:"column:stock_id"`
	FutureID    *int64    `gorm:"column:future_id"`
	LastRefresh time.Time `gorm:"column:last_refresh;not null;autoUpdateTime"`
	Price       int64     `gorm:"column:price;not null;default:0"`
	AskPrice    int64     `gorm:"column:ask_price;not null;default:0"`
	BidPrice    int64     `gorm:"column:bid_price;not null;default:0"`
}

func (Listing) TableName() string { return "listings" }

type ListingDailyPriceInfo struct {
	ID        int64     `gorm:"column:id;primaryKey"`
	ListingID int64     `gorm:"column:listing_id;not null"`
	Date      time.Time `gorm:"column:date;type:date;not null"`
	Price     int64     `gorm:"column:price;not null"`
	AskPrice  int64     `gorm:"column:ask_price;not null"`
	BidPrice  int64     `gorm:"column:bid_price;not null"`
	Change    int64     `gorm:"column:change;not null;default:0"`
	Volume    int64     `gorm:"column:volume;not null;default:0"`
}

func (ListingDailyPriceInfo) TableName() string { return "listing_daily_price_info" }

type OrderPlacer struct {
	ID         int64  `gorm:"column:id;primaryKey"`
	ClientID   *int64 `gorm:"column:client_id"`
	EmployeeID *int64 `gorm:"column:employee_id"`
}

func (OrderPlacer) TableName() string { return "order_placers" }

type Order struct {
	ID                int64          `gorm:"column:id;primaryKey"`
	PlacerID          int64          `gorm:"column:placer_id;not null"`
	ListingID         *int64         `gorm:"column:listing_id"`
	OptionID          *int64         `gorm:"column:option_id"`
	ForexPairID       *int64         `gorm:"column:forex_pair_id"`
	AccountNumber     string         `gorm:"column:account_number;type:varchar(20);not null"`
	OrderType         OrderType      `gorm:"column:order_type;type:order_type;not null"`
	Direction         OrderDirection `gorm:"column:direction;type:order_direction;not null"`
	Status            OrderStatus    `gorm:"column:status;type:order_status;not null;default:'pending'"`
	Quantity          int64          `gorm:"column:quantity;not null"`
	ContractSize      int64          `gorm:"column:contract_size;not null;default:1"`
	PricePerUnit      int64          `gorm:"column:price_per_unit;not null"`
	StopPrice         int64          `gorm:"column:stop_price;not null;default:0"`
	TriggeredAt       *time.Time     `gorm:"column:triggered_at"`
	RemainingPortions int64          `gorm:"column:remaining_portions;not null"`
	Commission        int64          `gorm:"column:commission;not null;default:0"`
	ApprovedBy        *int64         `gorm:"column:approved_by"`
	IsDone            bool           `gorm:"column:is_done;not null;default:false"`
	AfterHours        bool           `gorm:"column:after_hours;not null;default:false"`
	AllOrNone         bool           `gorm:"column:all_or_none;not null;default:false"`
	Margin            bool           `gorm:"column:margin;not null;default:false"`
	LastModification  time.Time      `gorm:"column:last_modification;not null;autoUpdateTime"`
	CreatedAt         time.Time      `gorm:"column:created_at;not null;autoCreateTime"`
}

func (Order) TableName() string { return "orders" }

// OrderFill is one chunk executed against an order by the market executor
// (#205). price_per_unit is in the instrument's currency; FxRate is set only
// for cross-currency fills and stores the rateInstrRSD/rateAccRSD used to
// convert the chunk cost into the placer's account currency.
type OrderFill struct {
	ID           int64     `gorm:"column:id;primaryKey"`
	OrderID      int64     `gorm:"column:order_id;not null"`
	Portions     int64     `gorm:"column:portions;not null"`
	PricePerUnit int64     `gorm:"column:price_per_unit;not null"`
	FxRate       *float64  `gorm:"column:fx_rate"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;autoCreateTime"`
}

func (OrderFill) TableName() string { return "order_fills" }

// Holding is the per-placer asset position written by the execution engine
// (#207). Polymorphic across the four asset kinds: exactly one of StockID,
// FutureID, ForexPairID, OptionID is set on every row (DB CHECK).
//
// AvgCost is denominated in the booking account's currency, which keeps the
// tax-tracking story self-contained: profit on a sell is simply
// (current_price_in_account_ccy - avg_cost) * amount, no per-row FX history
// required. AccountID is the destination for sell proceeds and is updated to
// the most-recent buy's account on each upsert.
//
// PublicAmount applies to stock holdings only and seeds the OTC counter (the
// actual OTC flow lives in the fourth celina); a CHECK constraint pins it to
// 0 for non-stock holdings.
type Holding struct {
	ID           int64     `gorm:"column:id;primaryKey"`
	PlacerID     int64     `gorm:"column:placer_id;not null"`
	StockID      *int64    `gorm:"column:stock_id"`
	FutureID     *int64    `gorm:"column:future_id"`
	ForexPairID  *int64    `gorm:"column:forex_pair_id"`
	OptionID     *int64    `gorm:"column:option_id"`
	AccountID    int64     `gorm:"column:account_id;not null"`
	Amount       int64     `gorm:"column:amount;not null;default:0"`
	AvgCost      int64     `gorm:"column:avg_cost;not null;default:0"`
	PublicAmount int64     `gorm:"column:public_amount;not null;default:0"`
	LastModified time.Time `gorm:"column:last_modified;not null;autoUpdateTime"`
}

func (Holding) TableName() string { return "holdings" }
