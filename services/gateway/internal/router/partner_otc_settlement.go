package router

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// =====================================================================
// Cross-bank OTC option settlement — envelope classification + parsing.
//
// Banka-4 (protocol-notes §2/§3) settles accepted OTC options through the
// same POST /interbank 2PC envelope as cash, but with multi-asset
// postings the cash path can't represent:
//
//   * accept   — premium (MONAS) + the option right (OPTION asset) moving
//                between PERSON accounts.
//   * exercise — strike (MONAS) + shares (STOCK) flowing through an
//                OPTION account (the contract acting as escrow).
//
// This file only CLASSIFIES and PARSES the envelope into the seller-side
// view (the postings that touch OUR routing number). The settlement
// itself is performed by the trading service; the gateway routing lives
// in partner_banka2.go.
// =====================================================================

// b2SettlementKind discriminates an inbound NEW_TX envelope.
type b2SettlementKind int

const (
	// b2KindCash — every posting is MONAS on an ACCOUNT/PERSON. The
	// existing cash 2PC path handles it.
	b2KindCash b2SettlementKind = iota
	// b2KindOTCAccept — carries an OPTION *asset* (the option right being
	// transferred on accept).
	b2KindOTCAccept
	// b2KindOTCExercise — carries an OPTION *account* (the contract escrow)
	// or a STOCK asset (share delivery on exercise).
	b2KindOTCExercise
)

// classifyB2Transaction decides which settlement path an envelope needs.
// OPTION asset wins (accept); otherwise an OPTION account or STOCK asset
// means exercise; otherwise it's a plain cash transfer.
func classifyB2Transaction(tx b2Transaction) b2SettlementKind {
	var optionAsset, stockAsset, optionAccount bool
	for i := range tx.Postings {
		switch tx.Postings[i].Asset.Type {
		case "OPTION":
			optionAsset = true
		case "STOCK":
			stockAsset = true
		}
		if tx.Postings[i].Account.Type == "OPTION" {
			optionAccount = true
		}
	}
	switch {
	case optionAsset:
		return b2KindOTCAccept
	case optionAccount || stockAsset:
		return b2KindOTCExercise
	default:
		return b2KindCash
	}
}

// Inner asset bodies (the `asset` blob inside a b2Asset).
type b2OptionAssetBody struct {
	NegotiationID b2ForeignID `json:"negotiationId"`
}

type b2StockAssetBody struct {
	Ticker string `json:"ticker"`
}

// b2OTCSettlement is the seller-side view of an accept/exercise envelope:
// only the postings whose account references OUR routing number. The
// counterparty postings (buyer's account/person at the other bank) are
// that bank's concern and are ignored here.
type b2OTCSettlement struct {
	Kind b2SettlementKind
	// OptionRef is the OTC reference minted by us (routing == ours):
	// the negotiationId on accept, the contractId on exercise.
	OptionRef b2ForeignID
	// SellerID is our seller PERSON (accept only — the option-right giver).
	SellerID b2ForeignID
	// CashAmount/CashCurrency is the MONAS leg credited to our side:
	// premium on accept, strike on exercise. Positive decimal string.
	CashAmount   string
	CashCurrency string
	// Ticker/Quantity describe the share leg on exercise.
	Ticker   string
	Quantity int64
	// BuyerSide is true when WE host the buyer (an OUTGOING thread): the
	// partner coordinates the accept 2PC and this NEW_TX debits our buyer's
	// premium (PERSON MONAS, amount<0) and credits them the option right
	// (PERSON OPTION, amount>0). Settled by debiting the buyer's bound
	// account; the contract is recorded by our accept SAGA. The seller-side
	// SellerID/Ticker/Quantity fields don't apply.
	BuyerSide bool
}

// b2AmountSign splits a wire amount into sign + absolute decimal string.
// Spec convention: negative = asset leaves, positive = asset enters.
func b2AmountSign(n json.Number) (positive bool, abs string) {
	s := strings.TrimSpace(string(n))
	switch {
	case strings.HasPrefix(s, "-"):
		return false, strings.TrimPrefix(s, "-")
	case strings.HasPrefix(s, "+"):
		return true, strings.TrimPrefix(s, "+")
	default:
		return true, s
	}
}

// parseB2OTCSettlement extracts the seller-side settlement intent from an
// accept/exercise envelope. ourRouting is this bank's routing number.
// Returns an error for a cash envelope or one with no posting touching us.
// parseB2OTCSettlement parses a Banka-2 OTC settlement envelope. ourRoutings
// lists every routing number that identifies US — our real BANK_ROUTING_NUMBER
// plus any presented routing a partner uses to address us (e.g. Banka-2 knows
// Banka-3 as 265), so PERSON/OPTION legs the partner addresses to us are
// recognized as local.
func parseB2OTCSettlement(tx b2Transaction, ourRoutings ...int) (*b2OTCSettlement, error) {
	kind := classifyB2Transaction(tx)
	out := &b2OTCSettlement{Kind: kind}

	ours := func(a b2TxAccount) bool {
		if a.ID == nil {
			return false
		}
		for _, rn := range ourRoutings {
			if a.ID.RoutingNumber == rn {
				return true
			}
		}
		return false
	}

	switch kind {
	case b2KindOTCAccept:
		for i := range tx.Postings {
			p := tx.Postings[i]
			if !ours(p.Account) {
				continue
			}
			switch {
			case p.Asset.Type == "OPTION" && p.Account.Type == "PERSON":
				// OPTION right on our PERSON. amount<0 = our seller GIVES the
				// right (we host the seller); amount>0 = our buyer RECEIVES it
				// (we host the buyer — partner-coordinated accept into us).
				out.OptionRef = optionNegotiationID(p.Asset)
				if pos, _ := b2AmountSign(p.Amount); pos {
					out.BuyerSide = true
				} else {
					out.SellerID = *p.Account.ID
				}
			case p.Asset.Type == "MONAS" && p.Account.Type == "PERSON":
				// Premium leg on our PERSON — amount>0 credits our seller,
				// amount<0 debits our buyer. Either way the magnitude is the
				// premium; the sign is captured by BuyerSide above.
				_, abs := b2AmountSign(p.Amount)
				out.CashAmount = abs
				out.CashCurrency = banka2CurrencyFromAsset(p.Asset)
			}
		}
		if out.OptionRef.ID == "" {
			return nil, fmt.Errorf("accept settlement: no OPTION posting for routings %v", ourRoutings)
		}

	case b2KindOTCExercise:
		for i := range tx.Postings {
			p := tx.Postings[i]
			if p.Account.Type != "OPTION" || !ours(p.Account) {
				continue
			}
			// Both the strike (MONAS) and the share delivery (STOCK) flow
			// through our contract escrow account.
			out.OptionRef = *p.Account.ID
			switch p.Asset.Type {
			case "MONAS":
				if pos, abs := b2AmountSign(p.Amount); pos {
					out.CashAmount = abs
					out.CashCurrency = banka2CurrencyFromAsset(p.Asset)
				}
			case "STOCK":
				_, abs := b2AmountSign(p.Amount)
				qty, qerr := strconv.ParseInt(abs, 10, 64)
				if qerr != nil {
					// No ctx in this parser — package-level slog.
					slog.Warn("otc exercise settlement: malformed stock quantity",
						"err", qerr, "amount", string(p.Amount))
				}
				out.Quantity = qty
				out.Ticker = stockTicker(p.Asset)
			}
		}
		if out.OptionRef.ID == "" {
			return nil, fmt.Errorf("exercise settlement: no OPTION account for routings %v", ourRoutings)
		}

	default:
		return nil, fmt.Errorf("not an OTC settlement envelope")
	}
	return out, nil
}

// optionNegotiationID reads the negotiationId out of an OPTION asset body.
func optionNegotiationID(a b2Asset) b2ForeignID {
	if a.Asset == nil {
		return b2ForeignID{}
	}
	var body b2OptionAssetBody
	if err := json.Unmarshal(*a.Asset, &body); err != nil {
		// No ctx in this parser — package-level slog. Returning the zero
		// id makes the caller report "no OPTION posting"; this log keeps
		// the real cause visible.
		slog.Warn("otc settlement: option asset body unmarshal failed",
			"err", err, "asset", bodySnippet(*a.Asset))
		return b2ForeignID{}
	}
	return body.NegotiationID
}

// stockTicker reads the ticker out of a STOCK asset body.
func stockTicker(a b2Asset) string {
	if a.Asset == nil {
		return ""
	}
	var body b2StockAssetBody
	if err := json.Unmarshal(*a.Asset, &body); err != nil {
		slog.Warn("otc settlement: stock asset body unmarshal failed",
			"err", err, "asset", bodySnippet(*a.Asset))
		return ""
	}
	return body.Ticker
}
