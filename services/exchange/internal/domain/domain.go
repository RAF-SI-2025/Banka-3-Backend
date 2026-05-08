// Package domain holds the exchange service's value types. No I/O.
package domain

import "time"

// Currency is the string-encoded enum stored in the DB. Values match
// the proto Currency enum tail; keep them in sync.
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

// Rate is one row of the FX table. Bid is the rate at which the bank
// *buys* the foreign currency from a client; Ask is the rate at which
// the bank *sells* it. Stored as decimal strings (numeric in the DB)
// to avoid float drift on small spreads.
type Rate struct {
	From      Currency
	To        Currency
	Bid       string
	Ask       string
	UpdatedAt time.Time
}
