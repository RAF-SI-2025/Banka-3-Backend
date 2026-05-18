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
	// c4 — money-moving OTC and fund flows. Each tag picks the same
	// executeMoneyMove engine but tags the ledger row so the Profit
	// Banke dashboard and pregled plaćanja can render the right
	// category.
	TxKindOTCPremium   TransactionKind = "otc_premium"
	TxKindOTCExercise  TransactionKind = "otc_exercise"
	TxKindFundInvest   TransactionKind = "fund_invest"
	TxKindFundWithdraw TransactionKind = "fund_withdraw"

	TxKindLoanDisbursement TransactionKind = "loan_disbursement"
	TxKindLoanInstallment  TransactionKind = "loan_installment"
)

// ReservationState pins the bank.reservations row lifecycle (c4).
type ReservationState string

const (
	ReservationHeld      ReservationState = "held"
	ReservationCommitted ReservationState = "committed"
	ReservationReleased  ReservationState = "released"
)

// Reservation is one row in bank.reservations — a held debit against
// available_balance pending the SAGA commit/release decision.
type Reservation struct {
	ID         string
	AccountID  string
	OpID       string
	Amount     string
	Currency   Currency
	State      ReservationState
	OpKind     string
	HeldAt     time.Time
	SettledAt  *time.Time
}

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
	// Resolved 18-digit numbers for From/ToAccountID. Populated only by
	// the read paths (ListTransactions / GetTransactionsByOpID); the
	// INSERT...RETURNING path leaves them empty (no counterparty join
	// at write time, and nobody reads them there).
	FromAccountNumber string
	ToAccountNumber   string
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
