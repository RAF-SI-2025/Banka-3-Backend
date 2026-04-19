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
	StatusPending  OrderStatus = "pending"
	StatusApproved OrderStatus = "approved"
	StatusDeclined OrderStatus = "declined"
	StatusDone     OrderStatus = "done"
)

type Exchange struct {
	ID             int64  `gorm:"column:id;primaryKey"`
	Name           string `gorm:"column:name;type:varchar(127);not null"`
	Acronym        string `gorm:"column:acronym;type:varchar(31);not null"`
	MICCode        string `gorm:"column:mic_code;type:varchar(8);not null;unique"`
	Polity         string `gorm:"column:polity;type:varchar(127);not null"`
	Currency       string `gorm:"column:currency;type:varchar(8);not null"`
	TimeZoneOffset string `gorm:"column:time_zone_offset;type:varchar(8);not null"`
	OpenOverride   bool   `gorm:"column:open_override;not null;default:false"`
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
	OrderType         OrderType      `gorm:"column:order_type;type:order_type;not null"`
	Direction         OrderDirection `gorm:"column:direction;type:order_direction;not null"`
	Status            OrderStatus    `gorm:"column:status;type:order_status;not null;default:'pending'"`
	Quantity          int64          `gorm:"column:quantity;not null"`
	ContractSize      int64          `gorm:"column:contract_size;not null;default:1"`
	PricePerUnit      int64          `gorm:"column:price_per_unit;not null"`
	RemainingPortions int64          `gorm:"column:remaining_portions;not null"`
	ApprovedBy        *int64         `gorm:"column:approved_by"`
	IsDone            bool           `gorm:"column:is_done;not null;default:false"`
	AfterHours        bool           `gorm:"column:after_hours;not null;default:false"`
	AllOrNone         bool           `gorm:"column:all_or_none;not null;default:false"`
	Margin            bool           `gorm:"column:margin;not null;default:false"`
	LastModification  time.Time      `gorm:"column:last_modification;not null;autoUpdateTime"`
	CreatedAt         time.Time      `gorm:"column:created_at;not null;autoCreateTime"`
}

func (Order) TableName() string { return "orders" }
