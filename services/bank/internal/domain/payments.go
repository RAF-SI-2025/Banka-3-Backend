package domain

import "time"

// =====================================================================
// Transactions ledger
// =====================================================================

type TransactionKind string

const (
	TxKindPayment  TransactionKind = "payment"
	TxKindTransfer TransactionKind = "transfer"
	TxKindExchange TransactionKind = "exchange"
	TxKindFee      TransactionKind = "fee"
	TxKindTrade    TransactionKind = "trade"
	TxKindTax      TransactionKind = "tax"
	TxKindForex    TransactionKind = "forex_fill"
)

type TransactionStatus string

const (
	TxStatusRealized   TransactionStatus = "realized"
	TxStatusRejected   TransactionStatus = "rejected"
	TxStatusProcessing TransactionStatus = "processing"
)

// Transaction is one ledger leg. UX-level operations group on OpID.
type Transaction struct {
	ID                string
	OpID              string
	Kind              TransactionKind
	LegIndex          int
	FromAccountID     string
	ToAccountID       string
	FromAmount        string
	ToAmount          string
	Rate              string // empty when from/to currencies match
	RecipientName     string
	PaymentCode       string
	ReferenceNumber   string
	Purpose           string
	InitiatorClientID string
	Status            TransactionStatus
	CreatedAt         time.Time
}

// TransactionFilter narrows a ListTransactions call.
type TransactionFilter struct {
	AccountID         string // any leg touching this account
	OpKind            string
	Status            string
	InitiatorClientID string
}

// PaymentResult bundles every leg of a single payment/transfer/exchange
// operation for the API response.
type PaymentResult struct {
	OpID         string
	Transactions []*Transaction
	Status       TransactionStatus
}

// =====================================================================
// Payment recipient template
// =====================================================================

type PaymentRecipient struct {
	ID            string
	ClientID      string
	Name          string
	AccountNumber string
	CreatedAt     time.Time
}

// =====================================================================
// Cards
// =====================================================================

type CardBrand string

const (
	BrandVisa       CardBrand = "visa"
	BrandMastercard CardBrand = "mastercard"
	BrandDinacard   CardBrand = "dinacard"
	BrandAmex       CardBrand = "amex"
)

type CardStatus string

const (
	CardActive      CardStatus = "active"
	CardBlocked     CardStatus = "blocked"
	CardDeactivated CardStatus = "deactivated"
)

type Card struct {
	ID                 string
	Number             string
	CVVHash            string
	Brand              CardBrand
	Name               string
	AccountID          string
	AuthorizedPersonID string // empty for personal
	CardLimit          string
	ExpiresAt          time.Time
	Status             CardStatus
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// =====================================================================
// Authorized persons (OvlascenoLice)
// =====================================================================

type AuthorizedPerson struct {
	ID          string
	CompanyID   string
	FirstName   string
	LastName    string
	DateOfBirth time.Time
	Gender      Gender
	Email       string
	Phone       string
	Address     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Gender is shared with the user service domain. Strings match the DB
// check constraint (`male`, `female`, `other`).
type Gender string

const (
	GenderUnspecified Gender = ""
	GenderMale        Gender = "male"
	GenderFemale      Gender = "female"
	GenderOther       Gender = "other"
)
