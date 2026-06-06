package domain

// ExternalOTCSettlement tracks an inbound cross-bank OTC option settlement
// 2PC (Banka-4 protocol-notes §2/§3): the multi-posting NEW_TX that moves
// the premium + option-right on accept, or the strike + shares on
// exercise. Recording it lets the gateway route the follow-up COMMIT_TX /
// ROLLBACK_TX back to trading (cash-only transfers stay in bank), and
// makes a re-delivered NEW_TX idempotent.
type ExternalOTCSettlement struct {
	SenderRoutingNumber int
	TransactionID       string // the partner's transaction id
	Kind                string // "accept" | "exercise"
	Status              string // "prepared" | "committed" | "rolled_back"
	OptionRef           string // negotiationId (accept) / contractId (exercise)
	ContractID          string // our local external_otc_contracts uuid, "" until formed
	Quantity            int64
	CashAmount          string // premium (accept) / strike (exercise), positive decimal
	CashCurrency        string
	OpID                string // bank cash-leg op id, stamped on commit
}

const (
	ExternalOTCSettlementKindAccept   = "accept"
	ExternalOTCSettlementKindExercise = "exercise"

	ExternalOTCSettlementPrepared   = "prepared"
	ExternalOTCSettlementCommitted  = "committed"
	ExternalOTCSettlementRolledBack = "rolled_back"
)
