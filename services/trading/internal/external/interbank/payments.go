// Outbound 2PC HTTP client — celina-5 cross-bank cash payments.
//
// Sibling of otc.go. Dispatches by partner protocol detection:
//
//   ProtocolNative — POST /bank/api/v1/interbank/transactions{...}
//     wire shape matches the gateway's partner_payments.go (native).
//   ProtocolBanka2 — POST /interbank with a Message<T> envelope
//     ({idempotenceKey, messageType, message}) per the Java
//     InterbankInboundController; reply is a TransactionVote on NEW_TX
//     or 204 on COMMIT_TX / ROLLBACK_TX.
//
// Idempotency:
//   * native — X-Idempotence-Key header per call (txID-prepare /
//     txID-commit / txID-rollback). The partner's gateway looks it up
//     in bank.interbank_protocol_messages on every request.
//   * banka2 — idempotenceKey field inside the envelope; partner
//     stashes the response keyed by (routingNumber, locallyGeneratedKey).

package interbank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

// =====================================================================
// Outbound input shapes (dialect-independent).
// =====================================================================

// PreparePartnerInput describes one outbound 2PC prepare leg. From
// our viewpoint (we're sending), LocalAccountNumber is the source
// account (ours, 333-prefixed) and RemoteAccountNumber is the dest
// (theirs, partner-prefixed).
type PreparePartnerInput struct {
	RemoteBankCode      string // partner routing, e.g. "222"
	TransactionID       string // our id; partner echoes
	LocalAccountNumber  string // ours, 18-digit
	RemoteAccountNumber string // partner's, 18-digit
	Currency            string // ISO short code, e.g. "EUR"
	Amount              string // positive decimal string
	Purpose             string
}

// PreparePartnerResult is the partner's reply. Accepted=false means
// the partner refused (banka2 NO vote or native 4xx). NoReasons is
// populated only by the banka2 dialect.
type PreparePartnerResult struct {
	Accepted  bool
	NoReasons []string
	RawStatus int
	RawBody   []byte
}

// CommitPartnerInput / RollbackPartnerInput re-use the partner
// routing + our transaction id to address the same prepared row on
// the partner's side.
type CommitPartnerInput struct {
	RemoteBankCode string
	TransactionID  string
}

type RollbackPartnerInput struct {
	RemoteBankCode string
	TransactionID  string
	Reason         string
}

// =====================================================================
// Dispatch.
// =====================================================================

// PreparePartner sends NEW_TX to the partner. Returns Accepted=true on
// a YES vote (banka2) or a 2xx with status="prepared" (native).
func (c *Client) PreparePartner(ctx context.Context, in PreparePartnerInput) (*PreparePartnerResult, error) {
	if c.baseURL(in.RemoteBankCode) == "" {
		return nil, &errUnknownBank{bankCode: in.RemoteBankCode}
	}
	switch c.Protocol(ctx, in.RemoteBankCode) {
	case ProtocolNative:
		return c.preparePartnerNative(ctx, in)
	case ProtocolBanka2:
		return c.preparePartnerBanka2(ctx, in)
	default:
		return nil, &errUnsupportedProtocol{bankCode: in.RemoteBankCode, protocol: ProtocolUnknown}
	}
}

// CommitPartner sends COMMIT_TX. Returns nil on success.
func (c *Client) CommitPartner(ctx context.Context, in CommitPartnerInput) error {
	if c.baseURL(in.RemoteBankCode) == "" {
		return &errUnknownBank{bankCode: in.RemoteBankCode}
	}
	switch c.Protocol(ctx, in.RemoteBankCode) {
	case ProtocolNative:
		return c.commitPartnerNative(ctx, in)
	case ProtocolBanka2:
		return c.commitPartnerBanka2(ctx, in)
	default:
		return &errUnsupportedProtocol{bankCode: in.RemoteBankCode, protocol: ProtocolUnknown}
	}
}

// RollbackPartner sends ROLLBACK_TX. Returns nil on success.
func (c *Client) RollbackPartner(ctx context.Context, in RollbackPartnerInput) error {
	if c.baseURL(in.RemoteBankCode) == "" {
		return &errUnknownBank{bankCode: in.RemoteBankCode}
	}
	switch c.Protocol(ctx, in.RemoteBankCode) {
	case ProtocolNative:
		return c.rollbackPartnerNative(ctx, in)
	case ProtocolBanka2:
		return c.rollbackPartnerBanka2(ctx, in)
	default:
		return &errUnsupportedProtocol{bankCode: in.RemoteBankCode, protocol: ProtocolUnknown}
	}
}

// =====================================================================
// Native dialect.
// =====================================================================

type nativePreparePaymentRequest struct {
	SenderRoutingNumber int    `json:"sender_routing_number"`
	TransactionID       string `json:"transaction_id"`
	Direction           string `json:"direction"` // partner's view: "inbound"
	LocalAccountNumber  string `json:"local_account_number"`
	RemoteAccountNumber string `json:"remote_account_number"`
	Currency            string `json:"currency"`
	Amount              string `json:"amount"`
	Purpose             string `json:"purpose"`
}

type nativePreparePaymentResponse struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
	ReservationID string `json:"reservation_id,omitempty"`
}

type nativeCommitPaymentRequest struct {
	SenderRoutingNumber int `json:"sender_routing_number"`
}

type nativeRollbackPaymentRequest struct {
	SenderRoutingNumber int    `json:"sender_routing_number"`
	Reason              string `json:"reason"`
}

func (c *Client) preparePartnerNative(ctx context.Context, in PreparePartnerInput) (*PreparePartnerResult, error) {
	ownRouting, _ := strconv.Atoi(c.cfg.OwnRoutingNumber)
	body := nativePreparePaymentRequest{
		SenderRoutingNumber: ownRouting,
		TransactionID:       in.TransactionID,
		Direction:           "inbound", // FROM PARTNER'S VIEW: they're crediting their user
		LocalAccountNumber:  in.RemoteAccountNumber,
		RemoteAccountNumber: in.LocalAccountNumber,
		Currency:            in.Currency,
		Amount:              in.Amount,
		Purpose:             in.Purpose,
	}
	url := c.baseURL(in.RemoteBankCode) + "/bank/api/v1/interbank/transactions"
	status, respBody, err := c.doJSONWithHeaders(ctx, "POST", url, body, map[string]string{
		"X-Idempotence-Key": in.TransactionID + "-prepare",
	})
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return &PreparePartnerResult{Accepted: false, RawStatus: status, RawBody: respBody}, partnerErrorFromBody(in.RemoteBankCode, status, respBody)
	}
	var parsed nativePreparePaymentResponse
	if err := jsonDecode(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("native prepare decode: %w", err)
	}
	accepted := strings.EqualFold(parsed.Status, "prepared")
	return &PreparePartnerResult{Accepted: accepted, RawStatus: status, RawBody: respBody}, nil
}

func (c *Client) commitPartnerNative(ctx context.Context, in CommitPartnerInput) error {
	ownRouting, _ := strconv.Atoi(c.cfg.OwnRoutingNumber)
	body := nativeCommitPaymentRequest{SenderRoutingNumber: ownRouting}
	url := c.baseURL(in.RemoteBankCode) + "/bank/api/v1/interbank/transactions/" + in.TransactionID + "/commit"
	status, respBody, err := c.doJSONWithHeaders(ctx, "POST", url, body, map[string]string{
		"X-Idempotence-Key": in.TransactionID + "-commit",
	})
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return partnerErrorFromBody(in.RemoteBankCode, status, respBody)
	}
	return nil
}

func (c *Client) rollbackPartnerNative(ctx context.Context, in RollbackPartnerInput) error {
	ownRouting, _ := strconv.Atoi(c.cfg.OwnRoutingNumber)
	body := nativeRollbackPaymentRequest{SenderRoutingNumber: ownRouting, Reason: in.Reason}
	url := c.baseURL(in.RemoteBankCode) + "/bank/api/v1/interbank/transactions/" + in.TransactionID + "/rollback"
	status, respBody, err := c.doJSONWithHeaders(ctx, "POST", url, body, map[string]string{
		"X-Idempotence-Key": in.TransactionID + "-rollback",
	})
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return partnerErrorFromBody(in.RemoteBankCode, status, respBody)
	}
	return nil
}

// =====================================================================
// Banka-2 dialect.
// =====================================================================

type b2IdempotenceKey struct {
	RoutingNumber       int    `json:"routingNumber"`
	LocallyGeneratedKey string `json:"locallyGeneratedKey"`
}

type b2ForeignID struct {
	RoutingNumber int    `json:"routingNumber"`
	ID            string `json:"id"`
}

type b2MonetaryAsset struct {
	Currency string `json:"currency"`
}

type b2Asset struct {
	Type  string          `json:"type"`
	Asset b2MonetaryAsset `json:"asset"`
}

type b2TxAccount struct {
	Type string `json:"type"`
	Num  string `json:"num"`
}

type b2Posting struct {
	Account b2TxAccount `json:"account"`
	Amount  json.Number `json:"amount"`
	Asset   b2Asset     `json:"asset"`
}

type b2Transaction struct {
	Postings       []b2Posting `json:"postings"`
	TransactionID  b2ForeignID `json:"transactionId"`
	Message        string      `json:"message"`
	CallNumber     string      `json:"callNumber"`
	PaymentCode    string      `json:"paymentCode"`
	PaymentPurpose string      `json:"paymentPurpose"`
}

type b2CommitMessage struct {
	TransactionID b2ForeignID `json:"transactionId"`
}

type b2Envelope struct {
	IdempotenceKey b2IdempotenceKey `json:"idempotenceKey"`
	MessageType    string           `json:"messageType"`
	Message        any              `json:"message"`
}

type b2NoVoteReason struct {
	Reason string `json:"reason"`
}

type b2TransactionVote struct {
	Vote    string           `json:"vote"`
	Reasons []b2NoVoteReason `json:"reasons,omitempty"`
}

func (c *Client) preparePartnerBanka2(ctx context.Context, in PreparePartnerInput) (*PreparePartnerResult, error) {
	ownRouting := c.presentedRouting(in.RemoteBankCode)
	partnerRouting, _ := strconv.Atoi(in.RemoteBankCode)
	tx := b2Transaction{
		TransactionID:  b2ForeignID{RoutingNumber: ownRouting, ID: in.TransactionID},
		PaymentPurpose: in.Purpose,
		Postings: []b2Posting{
			{
				Account: b2TxAccount{Type: "ACCOUNT", Num: in.LocalAccountNumber},
				Amount:  json.Number("-" + in.Amount),
				Asset:   b2Asset{Type: "MONAS", Asset: b2MonetaryAsset{Currency: in.Currency}},
			},
			{
				Account: b2TxAccount{Type: "ACCOUNT", Num: in.RemoteAccountNumber},
				Amount:  json.Number(in.Amount),
				Asset:   b2Asset{Type: "MONAS", Asset: b2MonetaryAsset{Currency: in.Currency}},
			},
		},
	}
	envelope := b2Envelope{
		IdempotenceKey: b2IdempotenceKey{RoutingNumber: ownRouting, LocallyGeneratedKey: in.TransactionID + "-prepare"},
		MessageType:    "NEW_TX",
		Message:        tx,
	}
	_ = partnerRouting
	url := c.baseURL(in.RemoteBankCode) + "/interbank"
	status, respBody, err := c.doJSON(ctx, "POST", url, envelope)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return &PreparePartnerResult{Accepted: false, RawStatus: status, RawBody: respBody},
			partnerErrorFromBody(in.RemoteBankCode, status, respBody)
	}
	var vote b2TransactionVote
	if err := jsonDecode(respBody, &vote); err != nil {
		return nil, fmt.Errorf("banka2 NEW_TX decode: %w", err)
	}
	if strings.EqualFold(vote.Vote, "YES") {
		return &PreparePartnerResult{Accepted: true, RawStatus: status, RawBody: respBody}, nil
	}
	reasons := make([]string, 0, len(vote.Reasons))
	for _, r := range vote.Reasons {
		reasons = append(reasons, r.Reason)
	}
	return &PreparePartnerResult{Accepted: false, NoReasons: reasons, RawStatus: status, RawBody: respBody}, nil
}

func (c *Client) commitPartnerBanka2(ctx context.Context, in CommitPartnerInput) error {
	ownRouting := c.presentedRouting(in.RemoteBankCode)
	envelope := b2Envelope{
		IdempotenceKey: b2IdempotenceKey{RoutingNumber: ownRouting, LocallyGeneratedKey: in.TransactionID + "-commit"},
		MessageType:    "COMMIT_TX",
		Message:        b2CommitMessage{TransactionID: b2ForeignID{RoutingNumber: ownRouting, ID: in.TransactionID}},
	}
	url := c.baseURL(in.RemoteBankCode) + "/interbank"
	status, respBody, err := c.doJSON(ctx, "POST", url, envelope)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return partnerErrorFromBody(in.RemoteBankCode, status, respBody)
	}
	return nil
}

func (c *Client) rollbackPartnerBanka2(ctx context.Context, in RollbackPartnerInput) error {
	ownRouting := c.presentedRouting(in.RemoteBankCode)
	envelope := b2Envelope{
		IdempotenceKey: b2IdempotenceKey{RoutingNumber: ownRouting, LocallyGeneratedKey: in.TransactionID + "-rollback"},
		MessageType:    "ROLLBACK_TX",
		Message:        b2CommitMessage{TransactionID: b2ForeignID{RoutingNumber: ownRouting, ID: in.TransactionID}},
	}
	url := c.baseURL(in.RemoteBankCode) + "/interbank"
	status, respBody, err := c.doJSON(ctx, "POST", url, envelope)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return partnerErrorFromBody(in.RemoteBankCode, status, respBody)
	}
	return nil
}

// =====================================================================
// service.PartnerPayer adapter.
// =====================================================================

// PreparePayment satisfies service.PartnerPayer — same as
// PreparePartner, with the result shape flattened.
func (c *Client) PreparePayment(ctx context.Context, in service.PartnerPaymentInput) (*service.PartnerPaymentResult, error) {
	r, err := c.PreparePartner(ctx, PreparePartnerInput{
		RemoteBankCode:      in.RemoteBankCode,
		TransactionID:       in.TransactionID,
		LocalAccountNumber:  in.LocalAccountNumber,
		RemoteAccountNumber: in.RemoteAccountNumber,
		Currency:            in.Currency,
		Amount:              in.Amount,
		Purpose:             in.Purpose,
	})
	if err != nil {
		return nil, err
	}
	return &service.PartnerPaymentResult{
		Accepted:  r.Accepted,
		NoReasons: r.NoReasons,
	}, nil
}

// CommitPayment satisfies service.PartnerPayer.
func (c *Client) CommitPayment(ctx context.Context, remoteBankCode, txID string) error {
	return c.CommitPartner(ctx, CommitPartnerInput{RemoteBankCode: remoteBankCode, TransactionID: txID})
}

// RollbackPayment satisfies service.PartnerPayer.
func (c *Client) RollbackPayment(ctx context.Context, remoteBankCode, txID, reason string) error {
	return c.RollbackPartner(ctx, RollbackPartnerInput{RemoteBankCode: remoteBankCode, TransactionID: txID, Reason: reason})
}

// =====================================================================
// doJSONWithHeaders — same as doJSON but attaches extra headers (used
// for native-dialect X-Idempotence-Key).
// =====================================================================

func (c *Client) doJSONWithHeaders(ctx context.Context, method, url string, body any, headers map[string]string) (int, []byte, error) {
	// Build the request ourselves so we can stamp headers before sending.
	// Mirrors doJSON; kept inline rather than refactored so the existing
	// OTC path stays byte-identical.
	if c.cfg.HTTPClient == nil {
		return 0, nil, fmt.Errorf("interbank: HTTP client not configured")
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(buf))
	if err != nil {
		return 0, nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if k := c.apiKeyForURL(url); k != "" {
		req.Header.Set("X-Api-Key", k)
	}
	c.signRequest(req, buf)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read body: %w", err)
	}
	return resp.StatusCode, out, nil
}
