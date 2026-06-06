package domain

import "time"

// ScheduledPaymentStatus is the lifecycle of a scheduled payment row.
// A row is created 'scheduled'; the due-sweep flips it to 'completed' or
// 'failed'; the owner can flip a still-'scheduled' row to 'cancelled'.
type ScheduledPaymentStatus string

const (
	ScheduledPaymentScheduled ScheduledPaymentStatus = "scheduled"
	ScheduledPaymentCompleted ScheduledPaymentStatus = "completed"
	ScheduledPaymentFailed    ScheduledPaymentStatus = "failed"
	ScheduledPaymentCancelled ScheduledPaymentStatus = "cancelled"
)

// ScheduledPayment is a one-time future-dated intra-bank payment
// (todoSpec C2 "Zakazivanje plaćanja"). The payment-shaped fields mirror
// CreatePayment's input so the due-sweep can replay the same money-move.
type ScheduledPayment struct {
	ID              string
	ClientID        string
	FromAccountID   string
	ToAccountNumber string
	Amount          string // in from-account currency, numeric(20,4)
	Currency        Currency
	RecipientName   string
	PaymentCode     string
	Purpose         string
	Model           string
	ReferenceNumber string
	ScheduledDate   time.Time
	Status          ScheduledPaymentStatus
	FailureReason   string
	CreatedAt       time.Time
	ExecutedAt      *time.Time
}
