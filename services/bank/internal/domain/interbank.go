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
	// InterbankTxPending is the transient state a transaction sits in
	// before its prepare decision is reached. The 2PC happy path goes
	// pending → prepared → committed/rolled_back; today PreparePayment
	// inserts straight to prepared, but the status flow models pending
	// so the supervisor view can show in-flight rows once the cross-bank
	// SAGA (sibling work) writes them. Numeric 0 on the wire.
	InterbankTxPending InterbankTxStatus = "pending"
	// InterbankTxFailed is written when prepare rejects (validation /
	// insufficient funds / blacklisted partner) so the supervisor sees
	// the attempt instead of nothing.
	InterbankTxFailed     InterbankTxStatus = "failed"
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

// InterbankBlacklistEntry is one row of "bank".interbank_blacklist —
// a partner bank (keyed by its routing number) that this bank refuses
// to transact with. Set either manually by a supervisor or
// automatically when consecutive partner failures cross the threshold.
// Active=false means the row was unblocked but is kept for audit.
type InterbankBlacklistEntry struct {
	SenderRoutingNumber int
	Reason              string
	// BlockedBy is the supervisor's user id for a manual block, or the
	// sentinel "system" for an auto-block.
	BlockedBy   string
	BlockedAt   time.Time
	UnblockedAt *time.Time
	Active      bool
}

// BlacklistAutoBlockBy is the BlockedBy marker stamped on an auto-block
// triggered by the consecutive-failure counter.
const BlacklistAutoBlockBy = "system"

// InterbankSupervisorAudience is the in-app notification recipient id
// used for interbank control events (auto-block). It's a well-known
// sentinel the FE/portal resolves to the supervisor audience rather than
// a concrete user — there's no single supervisor account to email.
const InterbankSupervisorAudience = "interbank-supervisors"

// InterbankFailureThreshold is the number of consecutive partner
// failures that auto-blocks a routing number. Spec p.77+ leaves the
// exact number to the implementation; three matches the verification
// "3 wrong attempts" cadence used elsewhere.
const InterbankFailureThreshold = 3

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
