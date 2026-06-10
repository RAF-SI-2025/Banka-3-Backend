package interbank

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

// =====================================================================
// Banka-2 dialect — OTC option EXERCISE (buyer-coordinated 2PC).
//
// Unlike accept (which the seller's bank coordinates), the BUYER drives
// exercise: we form the four-posting exercise transaction and run the
// §2 2PC against the seller's bank, which hosts the OPTION pseudo-account
// (the contract escrow). On COMMIT the seller's bank releases the
// reserved shares and credits the seller the strike; we settle our buyer
// legs (debit strike, credit shares) locally as coordinator.
//
//   p1 PERSON{us, buyerRef}      −total  MONAS{ccy}   buyer pays the strike   (local)
//   p2 OPTION{partner, contract} +total  MONAS{ccy}   credit contract→seller  (remote)
//   p3 OPTION{partner, contract} −qty    STOCK{tkr}   shares leave the escrow (remote)
//   p4 PERSON{us, buyerRef}      +qty    STOCK{tkr}   buyer receives shares   (local)
//
// Balanced per asset (MONAS: −total+total=0, STOCK: −qty+qty=0). The
// envelope + COMMIT_TX/ROLLBACK_TX reuse the cash-path plumbing.
// =====================================================================

// ExercisePartnerInput describes one outbound exercise against a partner.
type ExercisePartnerInput struct {
	RemoteBankCode string // partner (seller's bank) routing, e.g. "222"
	ContractID     string // the OTC reference the partner minted (== our remote_thread_id)
	TransactionID  string // our id; partner echoes it on COMMIT/ROLLBACK
	BuyerUserRef   string // our buyer's foreign id (the PERSON id we offered as)
	Quantity       int32
	StrikeTotal    string // strike × quantity, positive decimal string
	Currency       string // ISO code, e.g. "USD"
	Ticker         string
}

// Rich posting types for the multi-asset exercise envelope (the cash
// b2Posting only models MONAS on an ACCOUNT).
type b2ExAccount struct {
	Type string       `json:"type"`         // "PERSON" | "OPTION"
	ID   *b2ForeignID `json:"id,omitempty"` // foreign-bank id of the person / contract
}

type b2ExAsset struct {
	Type  string `json:"type"`  // "MONAS" | "STOCK"
	Asset any    `json:"asset"` // {currency} for MONAS, {ticker} for STOCK
}

type b2ExPosting struct {
	Account b2ExAccount `json:"account"`
	Amount  json.Number `json:"amount"`
	Asset   b2ExAsset   `json:"asset"`
}

type b2ExTransaction struct {
	Postings       []b2ExPosting `json:"postings"`
	TransactionID  b2ForeignID   `json:"transactionId"`
	Message        string        `json:"message"`
	PaymentCode    string        `json:"paymentCode"`
	PaymentPurpose string        `json:"paymentPurpose"`
}

// SpeaksBanka2Dialect satisfies service.PartnerOTC — true iff the partner
// speaks the Banka-2 / si-tx-proto envelope.
func (c *Client) SpeaksBanka2Dialect(ctx context.Context, bankCode string) bool {
	return c.Protocol(ctx, bankCode) == ProtocolBanka2
}

// AcceptCoordinatedByPartner satisfies service.PartnerOTC. For Banka-2 /
// si-tx-proto partners the SELLER's bank coordinates the OTC accept 2PC, so
// our buyer-side accept must NOT prepare its own premium payment (the partner
// debits our buyer via an inbound NEW_TX). Native partners coordinate from the
// buyer side → false.
func (c *Client) AcceptCoordinatedByPartner(ctx context.Context, bankCode string) bool {
	return c.Protocol(ctx, bankCode) == ProtocolBanka2
}

// ExerciseOption satisfies service.PartnerOTC: relay a buyer-coordinated
// exercise to a Banka-2 partner. Native partners don't reach here (the saga
// branches on SpeaksBanka2Dialect).
func (c *Client) ExerciseOption(ctx context.Context, in service.PartnerExerciseInput) error {
	return c.ExercisePartnerBanka2(ctx, ExercisePartnerInput{
		RemoteBankCode: in.RemoteBankCode,
		ContractID:     in.ContractID,
		TransactionID:  in.TransactionID,
		BuyerUserRef:   in.BuyerUserRef,
		Quantity:       in.Quantity,
		StrikeTotal:    in.StrikeTotal,
		Currency:       in.Currency,
		Ticker:         in.Ticker,
	})
}

// ExercisePartnerBanka2 runs the buyer-coordinated exercise 2PC against a
// Banka-2 dialect partner: NEW_TX → (YES ⇒ COMMIT_TX, NO ⇒ ROLLBACK_TX).
// Returns nil only when the partner committed.
func (c *Client) ExercisePartnerBanka2(ctx context.Context, in ExercisePartnerInput) error {
	ours := c.presentedRouting(in.RemoteBankCode)
	partner, _ := strconv.Atoi(in.RemoteBankCode)

	buyer := b2ForeignID{RoutingNumber: ours, ID: in.BuyerUserRef}
	contract := b2ForeignID{RoutingNumber: partner, ID: in.ContractID}
	monas := func() b2ExAsset { return b2ExAsset{Type: "MONAS", Asset: b2MonetaryAsset{Currency: in.Currency}} }
	stock := func() b2ExAsset { return b2ExAsset{Type: "STOCK", Asset: banka2Stock{Ticker: in.Ticker}} }
	qty := json.Number(strconv.Itoa(int(in.Quantity)))

	tx := b2ExTransaction{
		TransactionID:  b2ForeignID{RoutingNumber: ours, ID: in.TransactionID},
		Message:        "Peer OTC option exercise",
		PaymentCode:    "289",
		PaymentPurpose: "OTC option exercise",
		Postings: []b2ExPosting{
			{Account: b2ExAccount{Type: "PERSON", ID: &buyer}, Amount: json.Number("-" + in.StrikeTotal), Asset: monas()},
			{Account: b2ExAccount{Type: "OPTION", ID: &contract}, Amount: json.Number(in.StrikeTotal), Asset: monas()},
			{Account: b2ExAccount{Type: "OPTION", ID: &contract}, Amount: json.Number("-" + qty.String()), Asset: stock()},
			{Account: b2ExAccount{Type: "PERSON", ID: &buyer}, Amount: qty, Asset: stock()},
		},
	}

	envelope := b2Envelope{
		IdempotenceKey: b2IdempotenceKey{RoutingNumber: ours, LocallyGeneratedKey: in.TransactionID + "-prepare"},
		MessageType:    "NEW_TX",
		Message:        tx,
	}
	url := c.baseURL(in.RemoteBankCode) + "/interbank"
	status, body, err := c.doJSON(ctx, "POST", url, envelope)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return partnerErrorFromBody(in.RemoteBankCode, status, body)
	}
	var vote b2TransactionVote
	if err := jsonDecode(body, &vote); err != nil {
		return fmt.Errorf("banka2 exercise NEW_TX decode: %w", err)
	}
	if vote.Vote != "YES" {
		reasons := make([]string, 0, len(vote.Reasons))
		for _, rr := range vote.Reasons {
			reasons = append(reasons, rr.Reason)
		}
		// Roll the partner's prepared state back before surfacing the refusal.
		_ = c.rollbackPartnerBanka2(ctx, RollbackPartnerInput{RemoteBankCode: in.RemoteBankCode, TransactionID: in.TransactionID})
		return fmt.Errorf("banka2 exercise refused by %s: %v", in.RemoteBankCode, reasons)
	}
	return c.commitPartnerBanka2(ctx, CommitPartnerInput{RemoteBankCode: in.RemoteBankCode, TransactionID: in.TransactionID})
}
