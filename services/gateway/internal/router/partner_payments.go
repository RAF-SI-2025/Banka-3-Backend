// Partner-facing inbound payment surface — celina-5 2PC primitive
// over REST. Sibling of partner_otc.go.
//
// Three verbs map to bank's InterbankProtocolService:
//
//   POST /bank/api/v1/interbank/transactions               → PreparePayment
//   POST /bank/api/v1/interbank/transactions/{tx}/commit   → CommitPayment
//   POST /bank/api/v1/interbank/transactions/{tx}/rollback → RollbackPayment
//
// Idempotency is per-message: the partner sends a unique
// X-Idempotence-Key on each retry of the same payload. The gateway
// looks the key up in bank.interbank_protocol_messages first; on a
// hit, the cached HTTP status + body are replayed verbatim and the
// underlying RPC is not invoked.
//
// Auth is X-Api-Key (same shared secret as the OTC partner routes).
// JWT is bypassed by the /bank/ public-prefix.

package router

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	pkgauth "github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/signature"
)

// partnerCtx stamps the gateway's admin-sentinel principal as outgoing
// gRPC metadata so bank's requireInternal admits the call. Partner
// requests don't carry a JWT — the gateway authenticates them via
// X-Api-Key and translates into an admin internal call.
func partnerCtx(ctx context.Context) context.Context {
	return pkgauth.AttachToOutgoing(ctx, pkgauth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffc",
		UserKind:    pkgauth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
}

// PartnerPayments holds the gateway-side deps for the inbound payment
// surface. Set on Router when INTERBANK_API_KEY is configured and the
// InterbankProtocol client is reachable; otherwise the routes aren't
// registered.
type PartnerPayments struct {
	APIKey    string
	Interbank bankpb.InterbankProtocolServiceClient
	// SignKey enables celina-5 digital-signature verification on inbound
	// partner messages. When set, guard() verifies X-Timestamp +
	// X-Signature against the raw request body via pkg/signature and
	// rejects bad/missing/stale signatures with 401, in addition to the
	// X-Api-Key check. Empty disables verification so a dev stack without
	// INTERBANK_SIGN_KEY accepts unsigned partner traffic.
	SignKey string
}

// Wire types — native protocol, snake_case JSON.

type preparePaymentRequest struct {
	SenderRoutingNumber int    `json:"sender_routing_number"`
	TransactionID       string `json:"transaction_id"`
	// "inbound" = we credit a local account; "outbound" = we debit one.
	// The partner-facing path is almost always inbound (we are the
	// receiving bank when the partner initiates); outbound is included
	// for symmetry but rare.
	Direction           string `json:"direction"`
	LocalAccountNumber  string `json:"local_account_number"`
	RemoteAccountNumber string `json:"remote_account_number"`
	Currency            string `json:"currency"`
	Amount              string `json:"amount"`
	Purpose             string `json:"purpose"`
}

type preparePaymentResponse struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
	ReservationID string `json:"reservation_id,omitempty"`
}

type commitPaymentRequest struct {
	SenderRoutingNumber int `json:"sender_routing_number"`
}

type commitPaymentResponse struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
	OpID          string `json:"op_id,omitempty"`
}

type rollbackPaymentRequest struct {
	SenderRoutingNumber int    `json:"sender_routing_number"`
	Reason              string `json:"reason"`
}

type rollbackPaymentResponse struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
}

// MountPartnerPayments registers the three inbound payment routes.
// No-op when under-configured.
func (p *PartnerPayments) MountPartnerPayments(mux *http.ServeMux) {
	if p == nil || p.Interbank == nil || p.APIKey == "" {
		return
	}
	mux.HandleFunc("POST /bank/api/v1/interbank/transactions", p.guard(p.handlePrepare))
	mux.HandleFunc("POST /bank/api/v1/interbank/transactions/{transaction_id}/commit", p.guard(p.handleCommit))
	mux.HandleFunc("POST /bank/api/v1/interbank/transactions/{transaction_id}/rollback", p.guard(p.handleRollback))
}

func (p *PartnerPayments) guard(h http.HandlerFunc) http.HandlerFunc {
	expected := []byte(p.APIKey)
	verifier := signature.New(p.SignKey)
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("X-Api-Key"))
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid X-Api-Key")
			return
		}
		// Celina-5 digital signature: when a sign key is configured,
		// verify the timestamp + signature over the raw body before the
		// handler runs. The body is buffered and restored so downstream
		// handlers (which read r.Body themselves) are unaffected.
		if verifier.Enabled() {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			_ = r.Body.Close()
			if err != nil {
				writeError(w, http.StatusBadRequest, "read body: "+err.Error())
				return
			}
			if err := verifier.Verify(body, r.Header.Get("X-Timestamp"), r.Header.Get("X-Signature")); err != nil {
				writeError(w, http.StatusUnauthorized, "invalid signature: "+err.Error())
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		h(w, r)
	}
}

// directionFromWire maps the JSON tag to the proto enum.
func directionFromWire(s string) bankpb.InterbankPaymentDirection {
	switch s {
	case "inbound":
		return bankpb.InterbankPaymentDirection_INTERBANK_PAYMENT_DIRECTION_INBOUND
	case "outbound":
		return bankpb.InterbankPaymentDirection_INTERBANK_PAYMENT_DIRECTION_OUTBOUND
	}
	return bankpb.InterbankPaymentDirection_INTERBANK_PAYMENT_DIRECTION_UNSPECIFIED
}

func currencyFromWire(s string) bankpb.Currency {
	switch s {
	case "RSD":
		return bankpb.Currency_CURRENCY_RSD
	case "EUR":
		return bankpb.Currency_CURRENCY_EUR
	case "CHF":
		return bankpb.Currency_CURRENCY_CHF
	case "USD":
		return bankpb.Currency_CURRENCY_USD
	case "GBP":
		return bankpb.Currency_CURRENCY_GBP
	case "JPY":
		return bankpb.Currency_CURRENCY_JPY
	case "CAD":
		return bankpb.Currency_CURRENCY_CAD
	case "AUD":
		return bankpb.Currency_CURRENCY_AUD
	}
	return bankpb.Currency_CURRENCY_UNSPECIFIED
}

func statusToWire(s bankpb.InterbankTxStatus) string {
	switch s {
	case bankpb.InterbankTxStatus_INTERBANK_TX_STATUS_PREPARED:
		return "prepared"
	case bankpb.InterbankTxStatus_INTERBANK_TX_STATUS_COMMITTED:
		return "committed"
	case bankpb.InterbankTxStatus_INTERBANK_TX_STATUS_ROLLED_BACK:
		return "rolled_back"
	}
	return ""
}

// handlePrepare — POST /bank/api/v1/interbank/transactions.
func (p *PartnerPayments) handlePrepare(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var in preparePaymentRequest
	if err := json.Unmarshal(body, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if replayed := p.tryReplay(w, r, in.SenderRoutingNumber); replayed {
		return
	}
	resp, err := p.Interbank.PreparePayment(partnerCtx(r.Context()), &bankpb.PreparePaymentRequest{
		SenderRoutingNumber: int32(in.SenderRoutingNumber),
		TransactionId:       in.TransactionID,
		Direction:           directionFromWire(in.Direction),
		LocalAccountNumber:  in.LocalAccountNumber,
		RemoteAccountNumber: in.RemoteAccountNumber,
		Currency:            currencyFromWire(in.Currency),
		Amount:              in.Amount,
		TransactionBody:     string(body),
		Purpose:             in.Purpose,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	out := preparePaymentResponse{
		TransactionID: resp.GetTransactionId(),
		Status:        statusToWire(resp.GetStatus()),
		ReservationID: resp.GetReservationId(),
	}
	p.writeAndCache(w, r, in.SenderRoutingNumber, resp.GetTransactionId(),
		bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_NEW_TX, http.StatusOK, out)
}

// handleCommit — POST /bank/api/v1/interbank/transactions/{transaction_id}/commit.
func (p *PartnerPayments) handleCommit(w http.ResponseWriter, r *http.Request) {
	var in commitPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if replayed := p.tryReplay(w, r, in.SenderRoutingNumber); replayed {
		return
	}
	txID := r.PathValue("transaction_id")
	resp, err := p.Interbank.CommitPayment(partnerCtx(r.Context()), &bankpb.CommitPaymentRequest{
		SenderRoutingNumber: int32(in.SenderRoutingNumber),
		TransactionId:       txID,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	out := commitPaymentResponse{
		TransactionID: resp.GetTransactionId(),
		Status:        statusToWire(resp.GetStatus()),
		OpID:          resp.GetOpId(),
	}
	p.writeAndCache(w, r, in.SenderRoutingNumber, txID,
		bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_COMMIT_TX, http.StatusOK, out)
}

// handleRollback — POST /bank/api/v1/interbank/transactions/{transaction_id}/rollback.
func (p *PartnerPayments) handleRollback(w http.ResponseWriter, r *http.Request) {
	var in rollbackPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if replayed := p.tryReplay(w, r, in.SenderRoutingNumber); replayed {
		return
	}
	txID := r.PathValue("transaction_id")
	resp, err := p.Interbank.RollbackPayment(partnerCtx(r.Context()), &bankpb.RollbackPaymentRequest{
		SenderRoutingNumber: int32(in.SenderRoutingNumber),
		TransactionId:       txID,
		Reason:              in.Reason,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	out := rollbackPaymentResponse{
		TransactionID: resp.GetTransactionId(),
		Status:        statusToWire(resp.GetStatus()),
	}
	p.writeAndCache(w, r, in.SenderRoutingNumber, txID,
		bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_ROLLBACK_TX, http.StatusOK, out)
}

// tryReplay consults the inbound-message audit when X-Idempotence-Key
// is set. On a hit the cached response is written and true returned;
// on a miss (or no key) false is returned and the caller proceeds.
func (p *PartnerPayments) tryReplay(w http.ResponseWriter, r *http.Request, sender int) bool {
	key := r.Header.Get("X-Idempotence-Key")
	if key == "" || sender == 0 {
		return false
	}
	hit, err := p.Interbank.GetInboundMessage(partnerCtx(r.Context()), &bankpb.GetInboundMessageRequest{
		SenderRoutingNumber: int32(sender),
		IdempotenceKey:      key,
	})
	if err != nil || hit == nil || !hit.GetFound() {
		return false
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(int(hit.GetResponseStatus()))
	_, _ = w.Write([]byte(hit.GetResponseBody()))
	return true
}

// writeAndCache writes the JSON response and (when the partner sent
// X-Idempotence-Key) stashes it for replay. Cache write is best-effort.
func (p *PartnerPayments) writeAndCache(w http.ResponseWriter, r *http.Request, sender int, txID string, msgType bankpb.InterbankMessageType, status int, payload any) {
	buf, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal response: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
	key := r.Header.Get("X-Idempotence-Key")
	if key == "" || sender == 0 {
		return
	}
	_, _ = p.Interbank.RecordInboundMessage(partnerCtx(r.Context()), &bankpb.RecordInboundMessageRequest{
		SenderRoutingNumber: int32(sender),
		IdempotenceKey:      key,
		MessageType:         msgType,
		TransactionId:       txID,
		ResponseStatus:      int32(status),
		ResponseBody:        string(buf),
	})
}
