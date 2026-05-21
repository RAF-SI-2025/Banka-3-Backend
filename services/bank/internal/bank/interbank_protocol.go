package bank

import "encoding/json"

const (
	partnerPaymentProtocolNative = "native"
	partnerPaymentProtocolBanka2 = "banka2"
)

// These types are ported from the working Celina 5 integration. The fresh
// backend is gRPC-first, so they serve as the shared message/value contract
// that the future gateway partner handlers and bank gRPC methods will use.

type interbankProtocolEnvelope struct {
	IdempotenceKey interbankIdempotenceKey `json:"idempotenceKey"`
	MessageType    string                  `json:"messageType"`
	Message        json.RawMessage         `json:"message"`
}

type interbankIdempotenceKey struct {
	RoutingNumber       int    `json:"routingNumber"`
	LocallyGeneratedKey string `json:"locallyGeneratedKey"`
}

type interbankForeignBankID struct {
	RoutingNumber int    `json:"routingNumber"`
	ID            string `json:"id"`
}

type interbankTransaction struct {
	Postings       []interbankPosting     `json:"postings"`
	TransactionID  interbankForeignBankID `json:"transactionId"`
	Message        string                 `json:"message"`
	CallNumber     string                 `json:"callNumber,omitempty"`
	PaymentCode    string                 `json:"paymentCode"`
	PaymentPurpose string                 `json:"paymentPurpose"`
}

type interbankPosting struct {
	Account interbankAccount `json:"account"`
	Amount  json.Number      `json:"amount"`
	Asset   interbankAsset   `json:"asset"`
}

type interbankAccount struct {
	Type string                 `json:"type"`
	Num  string                 `json:"num,omitempty"`
	ID   *interbankForeignBankID `json:"id,omitempty"`
}

type interbankAsset struct {
	Type  string                     `json:"type"`
	Asset map[string]json.RawMessage `json:"asset"`
}

type interbankVote struct {
	Vote    string                  `json:"vote"`
	Reasons []interbankNoVoteReason `json:"reasons,omitempty"`
}

type interbankNoVoteReason struct {
	Reason string `json:"reason"`
}

type interbankCommitBody struct {
	TransactionID interbankForeignBankID `json:"transactionId"`
}

type interbankRollbackBody struct {
	TransactionID interbankForeignBankID `json:"transactionId"`
}
