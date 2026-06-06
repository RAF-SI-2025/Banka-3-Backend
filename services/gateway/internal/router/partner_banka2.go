// Banka-2 dialect inbound shim — celina-5 cross-bank surface for
// course-mate banks whose Spring Boot stack predates our native
// protocol. The outbound twin lives in
// services/trading/internal/external/interbank/banka2.go.
//
// Two surfaces:
//
//   POST   /interbank                          → 2PC Message envelope
//   GET    /public-stock                       → OTC discovery
//   POST   /negotiations                       → OTC create
//   GET    /negotiations/{rn}/{id}             → OTC read
//   PUT    /negotiations/{rn}/{id}             → OTC counter
//   DELETE /negotiations/{rn}/{id}             → OTC withdraw
//   GET    /negotiations/{rn}/{id}/accept      → OTC accept (sync 2PC)
//   GET    /bank/api/v1/interbank/user/{rn}/{id} → friendly-name lookup
//
// Banka-2 hardcodes the first six paths at the partner's base URL
// root, so we mount them at root too — they don't collide with our
// /api/v1/* (FE) or /bank/api/v1/* (native partner) namespaces. The
// user-info path is per-partner configurable on Banka-2's side; we
// publish it under /bank/api/v1/interbank/ so the Banka-2 operator
// just sets userInfoPath=/bank/api/v1/interbank/user/{rn}/{id}.
//
// Auth: X-Api-Key, same shared secret as PartnerPayments/PartnerOTC.
// Idempotency on POST /interbank: re-uses bank's interbank_protocol_
// messages table via Interbank.{Get,Record}InboundMessage keyed by
// (sender_routing, locally_generated_key).

package router

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PartnerBanka2 is the gateway-side glue for the Banka-2 inbound
// dialect. Mounts only when APIKey + the bank/trading/user clients
// it needs are all set.
type PartnerBanka2 struct {
	APIKey            string
	BankRoutingNumber string // ours, e.g. "333"
	BankDisplayName   string // ours, e.g. "Banka 3"

	Users      userpb.UserServiceClient
	Trading    tradingpb.TradingServiceClient
	TradingOTC tradingpb.ExternalOTCServiceClient
	Interbank  bankpb.InterbankProtocolServiceClient
}

// MountPartnerBanka2 registers the routes. No-op when under-configured.
func (p *PartnerBanka2) MountPartnerBanka2(mux *http.ServeMux) {
	if p == nil || p.APIKey == "" {
		return
	}
	if p.Interbank != nil {
		// §2 2PC envelope. Banka-4 and the canonical si-tx-proto layout
		// POST this to {peerBase}/interbank.
		mux.HandleFunc("POST /interbank", p.guard(p.handleEnvelope))
	}
	if p.TradingOTC != nil {
		// §3 OTC negotiation lifecycle. Mounted under BOTH layouts so a
		// partner's peer base URL can be our root either way:
		//   - "/negotiations…"            — older Banka-2 Spring shape
		//   - "/interbank/negotiations…"  — Banka-4 / canonical si-tx-proto
		//     (its peer client appends /interbank/… to the peer base URL)
		// Same handlers regardless of prefix.
		for _, pre := range []string{"", "/interbank"} {
			mux.HandleFunc("POST "+pre+"/negotiations", p.guard(p.handleCreateNegotiation))
			mux.HandleFunc("GET "+pre+"/negotiations/{routing_number}/{thread_id}", p.guard(p.handleReadNegotiation))
			mux.HandleFunc("PUT "+pre+"/negotiations/{routing_number}/{thread_id}", p.guard(p.handleCounterNegotiation))
			mux.HandleFunc("DELETE "+pre+"/negotiations/{routing_number}/{thread_id}", p.guard(p.handleCloseNegotiation))
			mux.HandleFunc("GET "+pre+"/negotiations/{routing_number}/{thread_id}/accept", p.guard(p.handleAcceptNegotiation))
		}
	}
	if p.Trading != nil {
		// Discovery is unauthenticated by spec §3.1 (the data is public-
		// by-definition) — matches our native /bank/api/v1/otc/public.
		// Both layouts (root + /interbank) for the same reason as above.
		mux.HandleFunc("GET /public-stock", p.handlePublicStock)
		mux.HandleFunc("GET /interbank/public-stock", p.handlePublicStock)
	}
	if p.Users != nil {
		// §3.7 user-info lookup. Banka-4 / canonical clients call
		// {peerBase}/interbank/user/{rn}/{id}. The legacy /bank/api/v1/…
		// mount stays for Banka-2 operators that pin a custom user-info-path.
		mux.HandleFunc("GET /interbank/user/{routing_number}/{user_id}", p.guard(p.handleUserInfo))
		mux.HandleFunc("GET /bank/api/v1/interbank/user/{routing_number}/{user_id}", p.guard(p.handleUserInfo))
	}
}

// guard validates X-Api-Key with constant-time compare.
func (p *PartnerBanka2) guard(h http.HandlerFunc) http.HandlerFunc {
	expected := []byte(p.APIKey)
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("X-Api-Key"))
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid X-Api-Key")
			return
		}
		h(w, r)
	}
}

// =====================================================================
// Banka-2 protocol shapes (mirrors of the Java records in
// rs.raf.banka2_bek.interbank.protocol).
// =====================================================================

type b2ForeignID struct {
	RoutingNumber int    `json:"routingNumber"`
	ID            string `json:"id"`
}

type b2Monetary struct {
	Currency string      `json:"currency"`
	Amount   json.Number `json:"amount"`
}

type b2Stock struct {
	Ticker string `json:"ticker"`
}

type b2MonetaryAsset struct {
	Currency string `json:"currency"`
}

// b2Asset is the Jackson-style tagged sum. Only `type` is required;
// the remaining fields are populated per the variant.
type b2Asset struct {
	Type  string           `json:"type"` // MONAS | STOCK | OPTION
	Asset *json.RawMessage `json:"asset,omitempty"`
}

// b2TxAccount is the tagged-sum account reference (PERSON|ACCOUNT|OPTION).
type b2TxAccount struct {
	Type string       `json:"type"`
	ID   *b2ForeignID `json:"id,omitempty"`  // PERSON, OPTION
	Num  string       `json:"num,omitempty"` // ACCOUNT
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

type b2CommitTransaction struct {
	TransactionID b2ForeignID `json:"transactionId"`
}

type b2RollbackTransaction struct {
	TransactionID b2ForeignID `json:"transactionId"`
}

type b2IdempotenceKey struct {
	RoutingNumber       int    `json:"routingNumber"`
	LocallyGeneratedKey string `json:"locallyGeneratedKey"`
}

type b2Envelope struct {
	IdempotenceKey b2IdempotenceKey `json:"idempotenceKey"`
	MessageType    string           `json:"messageType"`
	Message        json.RawMessage  `json:"message"`
}

type b2NoVoteReason struct {
	Reason  string     `json:"reason"`
	Posting *b2Posting `json:"posting,omitempty"`
}

type b2TransactionVote struct {
	Vote    string           `json:"vote"` // YES | NO
	Reasons []b2NoVoteReason `json:"reasons,omitempty"`
}

type b2OtcOffer struct {
	Stock          b2Stock     `json:"stock"`
	SettlementDate string      `json:"settlementDate"` // RFC3339
	PricePerUnit   b2Monetary  `json:"pricePerUnit"`
	Premium        b2Monetary  `json:"premium"`
	BuyerID        b2ForeignID `json:"buyerId"`
	SellerID       b2ForeignID `json:"sellerId"`
	Amount         json.Number `json:"amount"`
	LastModifiedBy b2ForeignID `json:"lastModifiedBy"`
}

type b2OtcNegotiation struct {
	Stock          b2Stock     `json:"stock"`
	SettlementDate string      `json:"settlementDate"`
	PricePerUnit   b2Monetary  `json:"pricePerUnit"`
	Premium        b2Monetary  `json:"premium"`
	BuyerID        b2ForeignID `json:"buyerId"`
	SellerID       b2ForeignID `json:"sellerId"`
	Amount         json.Number `json:"amount"`
	LastModifiedBy b2ForeignID `json:"lastModifiedBy"`
	IsOngoing      bool        `json:"isOngoing"`
}

type b2PublicStock struct {
	Stock   b2Stock          `json:"stock"`
	Sellers []b2PublicSeller `json:"sellers"`
}

type b2PublicSeller struct {
	Seller b2ForeignID `json:"seller"`
	Amount json.Number `json:"amount"`
}

type b2UserInformation struct {
	BankDisplayName string `json:"bankDisplayName"`
	DisplayName     string `json:"displayName"`
}

// =====================================================================
// POST /interbank — envelope dispatch.
// =====================================================================

func (p *PartnerBanka2) handleEnvelope(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var env b2Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid envelope JSON")
		return
	}
	if env.IdempotenceKey.LocallyGeneratedKey == "" || env.IdempotenceKey.RoutingNumber == 0 {
		writeError(w, http.StatusBadRequest, "envelope missing idempotenceKey")
		return
	}
	sender := env.IdempotenceKey.RoutingNumber

	// Idempotency replay — keyed by (sender_routing, locally_generated_key).
	hit, _ := p.Interbank.GetInboundMessage(partnerCtx(r.Context()), &bankpb.GetInboundMessageRequest{
		SenderRoutingNumber: int32(sender),
		IdempotenceKey:      env.IdempotenceKey.LocallyGeneratedKey,
	})
	if hit != nil && hit.GetFound() {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		st := int(hit.GetResponseStatus())
		if st == http.StatusNoContent {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(st)
		_, _ = w.Write([]byte(hit.GetResponseBody()))
		return
	}

	switch env.MessageType {
	case "NEW_TX":
		var tx b2Transaction
		if err := json.Unmarshal(env.Message, &tx); err != nil {
			writeError(w, http.StatusBadRequest, "NEW_TX: invalid Transaction body")
			return
		}
		p.handleNewTX(w, r, env.IdempotenceKey, tx)
	case "COMMIT_TX":
		var body b2CommitTransaction
		if err := json.Unmarshal(env.Message, &body); err != nil {
			writeError(w, http.StatusBadRequest, "COMMIT_TX: invalid body")
			return
		}
		p.handleCommitTX(w, r, env.IdempotenceKey, body)
	case "ROLLBACK_TX":
		var body b2RollbackTransaction
		if err := json.Unmarshal(env.Message, &body); err != nil {
			writeError(w, http.StatusBadRequest, "ROLLBACK_TX: invalid body")
			return
		}
		p.handleRollbackTX(w, r, env.IdempotenceKey, body)
	default:
		writeError(w, http.StatusBadRequest, "unknown messageType: "+env.MessageType)
	}
}

// handleNewTX translates a Banka-2 NEW_TX (cash payment subset) to our
// bank.PreparePayment. OTC-option exercise (Asset.OPTION on a
// TxAccount.OPTION pseudo-account) is rejected with UNACCEPTABLE_ASSET
// for now — the SAGA exercise path is not wired through the envelope.
func (p *PartnerBanka2) handleNewTX(w http.ResponseWriter, r *http.Request, key b2IdempotenceKey, tx b2Transaction) {
	if len(tx.Postings) == 0 {
		p.respondVoteNO(w, r, key, tx.TransactionID.ID, "UNBALANCED_TX", nil)
		return
	}
	// Reject anything with non-MONAS asset. OTC option exercise lives on
	// a separate path (the gateway's external-OTC saga). Bouncing here
	// keeps the cash-payment path simple and gives Banka-2 a structured
	// NO vote they can surface.
	for i := range tx.Postings {
		if tx.Postings[i].Asset.Type != "MONAS" {
			p.respondVoteNO(w, r, key, tx.TransactionID.ID, "UNACCEPTABLE_ASSET", &tx.Postings[i])
			return
		}
	}
	// Find the "ours" posting: a TxAccount.ACCOUNT whose 18-digit num
	// starts with our bank code. There must be exactly one.
	ourRouting := p.BankRoutingNumber
	if ourRouting == "" {
		ourRouting = "333"
	}
	var ourIdx = -1
	var otherIdx = -1
	for i, post := range tx.Postings {
		if post.Account.Type != "ACCOUNT" || post.Account.Num == "" {
			// PERSON / OPTION mid-payment — out of scope here.
			p.respondVoteNO(w, r, key, tx.TransactionID.ID, "UNACCEPTABLE_ASSET", &tx.Postings[i])
			return
		}
		if strings.HasPrefix(post.Account.Num, ourRouting) {
			if ourIdx != -1 {
				// Two of our accounts — would be an intra-bank tx, but a
				// remote initiated this so it's almost certainly wrong.
				p.respondVoteNO(w, r, key, tx.TransactionID.ID, "NO_SUCH_ACCOUNT", &tx.Postings[i])
				return
			}
			ourIdx = i
		} else {
			otherIdx = i
		}
	}
	if ourIdx == -1 {
		p.respondVoteNO(w, r, key, tx.TransactionID.ID, "NO_SUCH_ACCOUNT", nil)
		return
	}
	// Balance check (one currency only — multi-currency cross-bank is
	// out of scope for the cash path).
	if !postingsBalanced(tx.Postings) {
		p.respondVoteNO(w, r, key, tx.TransactionID.ID, "UNBALANCED_TX", nil)
		return
	}
	ours := tx.Postings[ourIdx]
	currency := banka2CurrencyFromAsset(ours.Asset)
	if currency == "" {
		p.respondVoteNO(w, r, key, tx.TransactionID.ID, "UNACCEPTABLE_ASSET", &tx.Postings[ourIdx])
		return
	}
	direction := bankpb.InterbankPaymentDirection_INTERBANK_PAYMENT_DIRECTION_INBOUND
	amountStr := strings.TrimPrefix(ours.Amount.String(), "+")
	if strings.HasPrefix(amountStr, "-") {
		direction = bankpb.InterbankPaymentDirection_INTERBANK_PAYMENT_DIRECTION_OUTBOUND
		amountStr = strings.TrimPrefix(amountStr, "-")
	}
	remoteAcc := ""
	if otherIdx >= 0 {
		remoteAcc = tx.Postings[otherIdx].Account.Num
	}
	purpose := strings.TrimSpace(strings.Join([]string{tx.PaymentPurpose, tx.Message}, " — "))

	resp, err := p.Interbank.PreparePayment(partnerCtx(r.Context()), &bankpb.PreparePaymentRequest{
		SenderRoutingNumber: int32(key.RoutingNumber),
		TransactionId:       tx.TransactionID.ID,
		Direction:           direction,
		LocalAccountNumber:  ours.Account.Num,
		RemoteAccountNumber: remoteAcc,
		Currency:            currencyFromWire(currency),
		Amount:              amountStr,
		Purpose:             purpose,
	})
	if err != nil {
		// Map gRPC-level rejections to NO votes with a best-guess reason.
		reason := banka2NoVoteFromGRPC(err)
		p.respondVoteNO(w, r, key, tx.TransactionID.ID, reason, nil)
		return
	}
	if resp.GetStatus() != bankpb.InterbankTxStatus_INTERBANK_TX_STATUS_PREPARED {
		p.respondVoteNO(w, r, key, tx.TransactionID.ID, "INSUFFICIENT_ASSET", nil)
		return
	}
	p.respondVoteYES(w, r, key, tx.TransactionID.ID)
}

func (p *PartnerBanka2) handleCommitTX(w http.ResponseWriter, r *http.Request, key b2IdempotenceKey, body b2CommitTransaction) {
	_, err := p.Interbank.CommitPayment(partnerCtx(r.Context()), &bankpb.CommitPaymentRequest{
		SenderRoutingNumber: int32(key.RoutingNumber),
		TransactionId:       body.TransactionID.ID,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	p.write204AndCache(w, r, key, body.TransactionID.ID, bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_COMMIT_TX)
}

func (p *PartnerBanka2) handleRollbackTX(w http.ResponseWriter, r *http.Request, key b2IdempotenceKey, body b2RollbackTransaction) {
	_, err := p.Interbank.RollbackPayment(partnerCtx(r.Context()), &bankpb.RollbackPaymentRequest{
		SenderRoutingNumber: int32(key.RoutingNumber),
		TransactionId:       body.TransactionID.ID,
		Reason:              "partner rollback",
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	p.write204AndCache(w, r, key, body.TransactionID.ID, bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_ROLLBACK_TX)
}

func (p *PartnerBanka2) respondVoteYES(w http.ResponseWriter, r *http.Request, key b2IdempotenceKey, txID string) {
	body, _ := json.Marshal(b2TransactionVote{Vote: "YES"})
	p.write200AndCache(w, r, key, txID, bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_NEW_TX, body)
}

func (p *PartnerBanka2) respondVoteNO(w http.ResponseWriter, r *http.Request, key b2IdempotenceKey, txID, reason string, posting *b2Posting) {
	body, _ := json.Marshal(b2TransactionVote{
		Vote:    "NO",
		Reasons: []b2NoVoteReason{{Reason: reason, Posting: posting}},
	})
	p.write200AndCache(w, r, key, txID, bankpb.InterbankMessageType_INTERBANK_MESSAGE_TYPE_NEW_TX, body)
}

func (p *PartnerBanka2) write200AndCache(w http.ResponseWriter, r *http.Request, key b2IdempotenceKey, txID string, msgType bankpb.InterbankMessageType, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	_, _ = p.Interbank.RecordInboundMessage(partnerCtx(r.Context()), &bankpb.RecordInboundMessageRequest{
		SenderRoutingNumber: int32(key.RoutingNumber),
		IdempotenceKey:      key.LocallyGeneratedKey,
		MessageType:         msgType,
		TransactionId:       txID,
		ResponseStatus:      http.StatusOK,
		ResponseBody:        string(body),
	})
}

func (p *PartnerBanka2) write204AndCache(w http.ResponseWriter, r *http.Request, key b2IdempotenceKey, txID string, msgType bankpb.InterbankMessageType) {
	w.WriteHeader(http.StatusNoContent)
	_, _ = p.Interbank.RecordInboundMessage(partnerCtx(r.Context()), &bankpb.RecordInboundMessageRequest{
		SenderRoutingNumber: int32(key.RoutingNumber),
		IdempotenceKey:      key.LocallyGeneratedKey,
		MessageType:         msgType,
		TransactionId:       txID,
		ResponseStatus:      http.StatusNoContent,
		ResponseBody:        "",
	})
}

func postingsBalanced(postings []b2Posting) bool {
	// Banka-2 BigDecimal-as-JSON-number can be either integer or
	// fractional. Use string addition by parsing to float64 — fine for
	// the protocol's two-decimal range (the spec forbids loss-of-
	// precision tricks but for *equality-to-zero* float is acceptable
	// at the cent scale, and we re-validate amounts on the bank side
	// via decimal math). This is a YES/NO gate, not a settlement step.
	totalsByCurrency := map[string]float64{}
	for i := range postings {
		ccy := banka2CurrencyFromAsset(postings[i].Asset)
		if ccy == "" {
			return false
		}
		amt, err := strconv.ParseFloat(postings[i].Amount.String(), 64)
		if err != nil {
			return false
		}
		totalsByCurrency[ccy] += amt
	}
	for _, v := range totalsByCurrency {
		if v < -0.005 || v > 0.005 {
			return false
		}
	}
	return true
}

func banka2CurrencyFromAsset(a b2Asset) string {
	if a.Type != "MONAS" || a.Asset == nil {
		return ""
	}
	var m b2MonetaryAsset
	if err := json.Unmarshal(*a.Asset, &m); err != nil {
		return ""
	}
	return m.Currency
}

func banka2NoVoteFromGRPC(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return "INSUFFICIENT_ASSET"
	}
	switch st.Code() {
	case codes.NotFound:
		return "NO_SUCH_ACCOUNT"
	case codes.FailedPrecondition, codes.ResourceExhausted:
		return "INSUFFICIENT_ASSET"
	case codes.InvalidArgument:
		return "UNACCEPTABLE_ASSET"
	}
	return "INSUFFICIENT_ASSET"
}

// =====================================================================
// OTC discovery — GET /public-stock.
// =====================================================================

func (p *PartnerBanka2) handlePublicStock(w http.ResponseWriter, r *http.Request) {
	resp, err := p.Trading.ListPublicHoldings(partnerCtx(r.Context()), &tradingpb.ListPublicHoldingsRequest{})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	ourRouting, _ := strconv.Atoi(p.BankRoutingNumber)
	byTicker := map[string]*b2PublicStock{}
	order := []string{}
	for _, it := range resp.GetItems() {
		t := it.GetSecurity().GetTicker()
		if t == "" {
			continue
		}
		ps, ok := byTicker[t]
		if !ok {
			ps = &b2PublicStock{Stock: b2Stock{Ticker: t}}
			byTicker[t] = ps
			order = append(order, t)
		}
		ps.Sellers = append(ps.Sellers, b2PublicSeller{
			Seller: b2ForeignID{RoutingNumber: ourRouting, ID: it.GetSellerId()},
			Amount: json.Number(strconv.Itoa(int(it.GetAvailableCount()))),
		})
	}
	out := make([]b2PublicStock, 0, len(order))
	for _, t := range order {
		out = append(out, *byTicker[t])
	}
	writeJSON(w, http.StatusOK, out)
}

// =====================================================================
// OTC negotiations.
// =====================================================================

func (p *PartnerBanka2) handleCreateNegotiation(w http.ResponseWriter, r *http.Request) {
	var offer b2OtcOffer
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		writeError(w, http.StatusBadRequest, "invalid OtcOffer JSON")
		return
	}
	settle, perr := parseBanka2Date(offer.SettlementDate)
	if perr != nil {
		writeError(w, http.StatusBadRequest, "settlementDate: "+perr.Error())
		return
	}
	// Banka-2's OtcOffer doesn't carry a sender-side thread id (their
	// protocol mints it on the seller's side). Our service needs one
	// for replay dedupe + (sender_bank, sender_thread) lookups on
	// subsequent counter/withdraw/accept calls. Synthesize one keyed
	// by the partner and persist via remote_thread_id.
	syntheticThreadID, err := synthesizeBanka2ThreadID(offer.BuyerID.RoutingNumber)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "thread-id gen: "+err.Error())
		return
	}
	// Banka-2's OtcOffer references the seller by user only; our
	// service expects the specific portfolio_holdings row id. Resolve
	// (ticker, seller_user_ref) → holding_id by walking ListPublicHoldings.
	holdingID, err := p.resolveBanka2Holding(r.Context(), offer.Stock.Ticker, offer.SellerID.ID)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	qty, _ := strconv.ParseInt(offer.Amount.String(), 10, 32)
	out, err := p.TradingOTC.ReceiveExternalOTCOffer(partnerCtx(r.Context()), &tradingpb.ReceiveExternalOTCOfferRequest{
		SenderBankCode:    strconv.Itoa(offer.BuyerID.RoutingNumber),
		SenderUserRef:     offer.BuyerID.ID,
		SenderDisplayName: "", // Banka-2 doesn't pass a display name; UI resolves via /user/{rn}/{id}.
		SenderThreadId:    syntheticThreadID,
		SellerHoldingId:   holdingID,
		Quantity:          int32(qty),
		PricePerUnit:      offer.PricePerUnit.Amount.String(),
		Premium:           offer.Premium.Amount.String(),
		SettlementDate:    timestamppb.New(settle),
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	ourRouting, _ := strconv.Atoi(p.BankRoutingNumber)
	writeJSON(w, http.StatusOK, b2ForeignID{
		RoutingNumber: ourRouting,
		ID:            out.GetLocalMirror().GetId(),
	})
}

func (p *PartnerBanka2) handleReadNegotiation(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("thread_id")
	if threadID == "" {
		writeError(w, http.StatusBadRequest, "thread_id required")
		return
	}
	resp, err := p.TradingOTC.GetExternalOTCThread(partnerCtx(r.Context()), &tradingpb.GetExternalOTCThreadRequest{
		ThreadId: threadID,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	t := resp.GetThread()
	if t == nil {
		writeError(w, http.StatusNotFound, "thread not found")
		return
	}
	// The "modified by" foreign-id is the side that last changed terms.
	// On a Banka-2-initiated thread the buyer is them (remote) and the
	// seller is us (local) — flip per modified_by_side. We approximate
	// "id" by the user ref because Banka-2's lastModifiedBy carries the
	// user/employee id, not the thread id.
	buyer := b2ForeignID{
		RoutingNumber: atoiOrZero(t.GetRemoteBankCode()),
		ID:            t.GetRemoteUserRef(),
	}
	ourRouting, _ := strconv.Atoi(p.BankRoutingNumber)
	seller := b2ForeignID{
		RoutingNumber: ourRouting,
		ID:            t.GetLocalUserId(),
	}
	modifiedBy := seller
	if t.GetModifiedBySide() == tradingpb.ExternalOTCSide_EXTERNAL_OTC_SIDE_REMOTE {
		modifiedBy = buyer
	}
	out := b2OtcNegotiation{
		Stock:          b2Stock{Ticker: t.GetSecurityTicker()},
		SettlementDate: t.GetSettlementDate().AsTime().Format(time.RFC3339),
		PricePerUnit:   b2Monetary{Currency: currencyShortString(t.GetCurrency()), Amount: json.Number(t.GetPricePerUnit())},
		Premium:        b2Monetary{Currency: currencyShortString(t.GetCurrency()), Amount: json.Number(t.GetPremium())},
		BuyerID:        buyer,
		SellerID:       seller,
		Amount:         json.Number(strconv.Itoa(int(t.GetQuantity()))),
		LastModifiedBy: modifiedBy,
		IsOngoing:      t.GetStatus() == tradingpb.ExternalOTCThreadStatus_EXTERNAL_OTC_THREAD_STATUS_OPEN,
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *PartnerBanka2) handleCounterNegotiation(w http.ResponseWriter, r *http.Request) {
	var offer b2OtcOffer
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		writeError(w, http.StatusBadRequest, "invalid OtcOffer JSON")
		return
	}
	settle, perr := parseBanka2Date(offer.SettlementDate)
	if perr != nil {
		writeError(w, http.StatusBadRequest, "settlementDate: "+perr.Error())
		return
	}
	threadID := r.PathValue("thread_id")
	senderBank, senderThread, err := p.lookupRemoteRef(r.Context(), threadID)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	qty, _ := strconv.ParseInt(offer.Amount.String(), 10, 32)
	_, err = p.TradingOTC.ReceiveExternalOTCCounter(partnerCtx(r.Context()), &tradingpb.ReceiveExternalOTCCounterRequest{
		SenderBankCode: senderBank,
		SenderThreadId: senderThread,
		Quantity:       int32(qty),
		PricePerUnit:   offer.PricePerUnit.Amount.String(),
		Premium:        offer.Premium.Amount.String(),
		SettlementDate: timestamppb.New(settle),
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *PartnerBanka2) handleCloseNegotiation(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("thread_id")
	senderBank, senderThread, err := p.lookupRemoteRef(r.Context(), threadID)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	_, err = p.TradingOTC.ReceiveExternalOTCWithdraw(partnerCtx(r.Context()), &tradingpb.ReceiveExternalOTCActionRequest{
		SenderBankCode: senderBank,
		SenderThreadId: senderThread,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *PartnerBanka2) handleAcceptNegotiation(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("thread_id")
	senderBank, senderThread, err := p.lookupRemoteRef(r.Context(), threadID)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	// Banka-2 spec §3.6: blocks until 2PC finishes; on failure surfaces
	// as 4xx/5xx. Our ReceiveExternalOTCAccept is synchronous over the
	// SAGA accept flow, so the wait is built in.
	_, err = p.TradingOTC.ReceiveExternalOTCAccept(partnerCtx(r.Context()), &tradingpb.ReceiveExternalOTCActionRequest{
		SenderBankCode: senderBank,
		SenderThreadId: senderThread,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveBanka2Holding walks ListPublicHoldings to translate a
// (ticker, seller-user-uuid) pair from the Banka-2 dialect into our
// service's holding_id. Returns NotFound when nothing matches.
func (p *PartnerBanka2) resolveBanka2Holding(ctx context.Context, ticker, sellerUserID string) (string, error) {
	resp, err := p.Trading.ListPublicHoldings(partnerCtx(ctx), &tradingpb.ListPublicHoldingsRequest{Ticker: ticker})
	if err != nil {
		return "", err
	}
	for _, it := range resp.GetItems() {
		if it.GetSellerId() == sellerUserID && it.GetSecurity().GetTicker() == ticker {
			return it.GetHoldingId(), nil
		}
	}
	return "", status.Errorf(codes.NotFound, "no public holding for ticker=%s seller=%s", ticker, sellerUserID)
}

// lookupRemoteRef returns (remote_bank_code, remote_thread_id) for a
// thread whose local id we received in the URL. Banka-2 only knows our
// local id (we minted it on /negotiations POST), so we need to bounce
// through the service to translate it back into the (sender_bank,
// sender_thread) pair the receive-side handlers expect.
func (p *PartnerBanka2) lookupRemoteRef(ctx context.Context, threadID string) (string, string, error) {
	resp, err := p.TradingOTC.GetExternalOTCThread(partnerCtx(ctx), &tradingpb.GetExternalOTCThreadRequest{
		ThreadId: threadID,
	})
	if err != nil {
		return "", "", err
	}
	t := resp.GetThread()
	if t == nil {
		return "", "", status.Error(codes.NotFound, "thread not found")
	}
	return t.GetRemoteBankCode(), t.GetRemoteThreadId(), nil
}

// =====================================================================
// User-info — GET /bank/api/v1/interbank/user/{rn}/{id}.
// =====================================================================

func (p *PartnerBanka2) handleUserInfo(w http.ResponseWriter, r *http.Request) {
	wantRouting := r.PathValue("routing_number")
	wantID := r.PathValue("user_id")
	ours := p.BankRoutingNumber
	if ours == "" {
		ours = "333"
	}
	if wantRouting != ours {
		writeError(w, http.StatusNotFound, "routing number is not ours")
		return
	}
	// Try client first, then employee. Both share the UUID id space.
	display := ""
	if c, err := p.Users.GetClient(partnerCtx(r.Context()), &userpb.GetClientRequest{Id: wantID}); err == nil && c != nil {
		display = strings.TrimSpace(c.GetFirstName() + " " + c.GetLastName())
	}
	if display == "" {
		if e, err := p.Users.GetEmployee(partnerCtx(r.Context()), &userpb.GetEmployeeRequest{Id: wantID}); err == nil && e != nil {
			display = strings.TrimSpace(e.GetFirstName() + " " + e.GetLastName())
		}
	}
	if display == "" {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	bankName := p.BankDisplayName
	if bankName == "" {
		bankName = "Banka 3"
	}
	writeJSON(w, http.StatusOK, b2UserInformation{
		BankDisplayName: bankName,
		DisplayName:     display,
	})
}

// =====================================================================
// Small helpers.
// =====================================================================

func parseBanka2Date(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("expected RFC3339 or YYYY-MM-DD, got %q", s)
}

func atoiOrZero(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// synthesizeBanka2ThreadID mints a deterministically-shaped string we
// can re-use as the sender_thread_id for a Banka-2 originated OTC
// thread. Banka-2 doesn't carry a sender-thread-id, so this lives on
// our side only. Format: "b2-{routing}-{16 hex chars}".
func synthesizeBanka2ThreadID(routing int) (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("b2-%d-%s", routing, hex.EncodeToString(buf[:])), nil
}
