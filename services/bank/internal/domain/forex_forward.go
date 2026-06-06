package domain

import "time"

// =====================================================================
// Forex forwards (terminski valutni ugovori, todoSpec C3)
// =====================================================================

// ForexForwardStatus pins the bank.forex_forwards row lifecycle.
type ForexForwardStatus string

const (
	// ForexForwardActive — concluded, quote-currency obligation reserved,
	// awaiting its settlement date.
	ForexForwardActive ForexForwardStatus = "active"
	// ForexForwardSettled — the settlement sweep performed the direct
	// fixed-rate conversion successfully.
	ForexForwardSettled ForexForwardStatus = "settled"
	// ForexForwardCancelled — the client cancelled while still active; the
	// reservation was released.
	ForexForwardCancelled ForexForwardStatus = "cancelled"
	// ForexForwardFailed — the settlement sweep could not complete (e.g.
	// the reserved funds were already spent through another path); the
	// reservation, if still held, is released.
	ForexForwardFailed ForexForwardStatus = "failed"
)

// ForexForwardSpread is the per-pair annualised spread factor used in the
// forward-rate formula. Set by supervisors.
type ForexForwardSpread struct {
	BaseCurrency  Currency
	QuoteCurrency Currency
	SpreadFactor  string // decimal string, e.g. "0.02" = 2%/yr
	UpdatedBy     string
	UpdatedAt     time.Time
}

// ForexForward is one concluded forward contract.
type ForexForward struct {
	ID               string
	ClientID         string
	BaseCurrency     Currency // notional currency (bought forward, credited at settlement)
	QuoteCurrency    Currency // settlement-leg currency (RSD; debited at settlement)
	Notional         string
	ForwardRate      string // locked quote-per-1-base
	SpotAskRate      string // spot ASK at conclusion (quote per 1 base)
	SpreadFactor     string
	DaysToSettlement int
	Commission       string // in quote currency
	ReservationID    string // op_id of the held reservation on the quote account
	FromAccountID    string // client's quote (RSD) account
	ToAccountID      string // client's base-currency account
	SettlementDate   time.Time
	Status           ForexForwardStatus
	FailureReason    string
	CreatedAt        time.Time
	SettledAt        *time.Time
}
