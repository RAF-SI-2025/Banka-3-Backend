package router

import (
	"encoding/json"
	"fmt"
	"testing"
)

func monasAsset(cur string) b2Asset {
	raw := json.RawMessage(fmt.Sprintf(`{"currency":%q}`, cur))
	return b2Asset{Type: "MONAS", Asset: &raw}
}

func optionAssetOf(routing int, negID string) b2Asset {
	raw := json.RawMessage(fmt.Sprintf(`{"negotiationId":{"routingNumber":%d,"id":%q}}`, routing, negID))
	return b2Asset{Type: "OPTION", Asset: &raw}
}

func stockAssetOf(ticker string) b2Asset {
	raw := json.RawMessage(fmt.Sprintf(`{"ticker":%q}`, ticker))
	return b2Asset{Type: "STOCK", Asset: &raw}
}

func personAcct(routing int, id string) b2TxAccount {
	return b2TxAccount{Type: "PERSON", ID: &b2ForeignID{RoutingNumber: routing, ID: id}}
}

func optionAcct(routing int, id string) b2TxAccount {
	return b2TxAccount{Type: "OPTION", ID: &b2ForeignID{RoutingNumber: routing, ID: id}}
}

func numAcct(num string) b2TxAccount {
	return b2TxAccount{Type: "ACCOUNT", Num: num}
}

// Accept envelope per protocol-notes: buyer 444, seller 333 (us).
func acceptTx() b2Transaction {
	return b2Transaction{Postings: []b2Posting{
		{Account: numAcct("444000000000000011"), Amount: "-2", Asset: monasAsset("USD")},
		{Account: personAcct(333, "10"), Amount: "2", Asset: monasAsset("USD")},
		{Account: personAcct(333, "10"), Amount: "-1", Asset: optionAssetOf(333, "neg1")},
		{Account: personAcct(444, "buyer1"), Amount: "1", Asset: optionAssetOf(333, "neg1")},
	}}
}

// Exercise envelope per protocol-notes: contract c1 at seller 333 (us).
func exerciseTx() b2Transaction {
	return b2Transaction{Postings: []b2Posting{
		{Account: numAcct("444000000000000011"), Amount: "-440", Asset: monasAsset("USD")},
		{Account: optionAcct(333, "c1"), Amount: "440", Asset: monasAsset("USD")},
		{Account: optionAcct(333, "c1"), Amount: "-10", Asset: stockAssetOf("CBSH")},
		{Account: personAcct(444, "buyer1"), Amount: "10", Asset: stockAssetOf("CBSH")},
	}}
}

func cashTx() b2Transaction {
	return b2Transaction{Postings: []b2Posting{
		{Account: numAcct("444000000000000011"), Amount: "-100", Asset: monasAsset("RSD")},
		{Account: numAcct("333000000000000011"), Amount: "100", Asset: monasAsset("RSD")},
	}}
}

func TestClassifyB2Transaction(t *testing.T) {
	if got := classifyB2Transaction(acceptTx()); got != b2KindOTCAccept {
		t.Errorf("accept: got kind %d, want OTCAccept", got)
	}
	if got := classifyB2Transaction(exerciseTx()); got != b2KindOTCExercise {
		t.Errorf("exercise: got kind %d, want OTCExercise", got)
	}
	if got := classifyB2Transaction(cashTx()); got != b2KindCash {
		t.Errorf("cash: got kind %d, want Cash", got)
	}
}

func TestParseB2OTCSettlement_Accept(t *testing.T) {
	s, err := parseB2OTCSettlement(acceptTx(), 333)
	if err != nil {
		t.Fatalf("accept parse: %v", err)
	}
	if s.Kind != b2KindOTCAccept {
		t.Errorf("kind: got %d", s.Kind)
	}
	if s.OptionRef.RoutingNumber != 333 || s.OptionRef.ID != "neg1" {
		t.Errorf("optionRef: got %+v, want {333 neg1}", s.OptionRef)
	}
	if s.SellerID.RoutingNumber != 333 || s.SellerID.ID != "10" {
		t.Errorf("sellerID: got %+v, want {333 10}", s.SellerID)
	}
	if s.CashAmount != "2" || s.CashCurrency != "USD" {
		t.Errorf("premium: got %s %s, want 2 USD", s.CashAmount, s.CashCurrency)
	}
}

func TestParseB2OTCSettlement_Exercise(t *testing.T) {
	s, err := parseB2OTCSettlement(exerciseTx(), 333)
	if err != nil {
		t.Fatalf("exercise parse: %v", err)
	}
	if s.Kind != b2KindOTCExercise {
		t.Errorf("kind: got %d", s.Kind)
	}
	if s.OptionRef.RoutingNumber != 333 || s.OptionRef.ID != "c1" {
		t.Errorf("optionRef (contractId): got %+v, want {333 c1}", s.OptionRef)
	}
	if s.CashAmount != "440" || s.CashCurrency != "USD" {
		t.Errorf("strike: got %s %s, want 440 USD", s.CashAmount, s.CashCurrency)
	}
	if s.Ticker != "CBSH" || s.Quantity != 10 {
		t.Errorf("shares: got %s x%d, want CBSH x10", s.Ticker, s.Quantity)
	}
}

func TestParseB2OTCSettlement_CashRejected(t *testing.T) {
	if _, err := parseB2OTCSettlement(cashTx(), 333); err == nil {
		t.Errorf("cash envelope should not parse as OTC settlement")
	}
}
