package domain

import "time"

// =====================================================================
// Inter-bank 2PC primitive (celina 5 — spec p.77+).
// =====================================================================

// InterbankPaymentDirection is the local view of a cross-bank payment.
type InterbankPaymentDirection string

const (
	// InterbankInbound — we are the receiving bank; partner's debit
	// supplies funds we'll credit to a local account.
	InterbankInbound InterbankPaymentDirection = "inbound"
	// InterbankOutbound — we are the sending bank; local user's account
	// is debited on prepare, partner's system absorbs the credit on
	// commit.
	InterbankOutbound InterbankPaymentDirection = "outbound"
)

// InterbankTxStatus pins the lifecycle of a 2PC transaction.
type InterbankTxStatus string

const (
	InterbankTxPrepared   InterbankTxStatus = "prepared"
	InterbankTxCommitted  InterbankTxStatus = "committed"
	InterbankTxRolledBack InterbankTxStatus = "rolled_back"
)

// InterbankMessageType maps to the partner's wire-format message tag.
type InterbankMessageType string

const (
	InterbankMsgNewTx      InterbankMessageType = "NEW_TX"
	InterbankMsgCommitTx   InterbankMessageType = "COMMIT_TX"
	InterbankMsgRollbackTx InterbankMessageType = "ROLLBACK_TX"
)

// InterbankProtocolTransaction is one row of
// "bank".interbank_protocol_transactions. Primary key is the partner-
// supplied (sender_routing_number, transaction_id) tuple; we don't
// validate transaction_id as a uuid because partner banks may use
// arbitrary identifier formats.
type InterbankProtocolTransaction struct {
	SenderRoutingNumber int
	TransactionID       string

	Direction           InterbankPaymentDirection
	LocalAccountNumber  string
	RemoteAccountNumber string
	Currency            Currency
	Amount              string
	Purpose             string

	TransactionBody string

	// ReservationID is non-empty when direction=outbound and prepare
	// succeeded — points at the bank.reservations row that holds the
	// debit until commit / rollback.
	ReservationID string
	// OpID is set at commit time — the bank.transactions op_id of the
	// settled leg.
	OpID string

	Status    InterbankTxStatus
	LastError string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// InterbankProtocolMessage caches the verbatim response we sent to a
// partner for a given (sender_routing_number, idempotence_key). The
// gateway looks this up before processing an inbound message; on a
// hit, the cached response is replayed and the underlying handler is
// not invoked.
type InterbankProtocolMessage struct {
	SenderRoutingNumber int
	IdempotenceKey      string

	MessageType    InterbankMessageType
	TransactionID  string
	ResponseStatus int
	ResponseBody   string

	CreatedAt time.Time
	UpdatedAt time.Time
}
