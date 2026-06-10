// Command mock-partner is a tiny HTTP server that pretends to be a
// rival Banka 3 instance. It speaks the celina-5 native protocol on
// /bank/api/v1/otc/... so the dev stack can drive end-to-end
// cross-bank OTC flows without spinning up two real instances.
//
// State is in-memory and reset on each restart — every booted mock
// advertises the same canned inventory (one AAPL holding) and any
// threads we receive get a deterministic id assigned for replay
// stability in cypress specs.
//
// Auth: X-Api-Key gates every mutating route; reads (the public
// discovery feed) are unauthenticated by design so our gateway's
// protocol-detection probe works without sharing the API key first.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------
// Wire types — exactly mirror trading/internal/external/interbank/otc.go.
// ---------------------------------------------------------------------

type publicHolding struct {
	BankCode          string `json:"bank_code"`
	SellerUserRef     string `json:"seller_user_ref"`
	SellerDisplayName string `json:"seller_display_name"`
	SellerHoldingID   string `json:"seller_holding_id"`
	SecurityTicker    string `json:"security_ticker"`
	SecurityType      string `json:"security_type"`
	Currency          string `json:"currency"`
	Quantity          int32  `json:"quantity"`
	AskPrice          string `json:"ask_price"`
	Premium           string `json:"premium"`
}

type offerRequest struct {
	SenderBankCode    string `json:"sender_bank_code"`
	SenderUserRef     string `json:"sender_user_ref"`
	SenderDisplayName string `json:"sender_display_name"`
	SenderThreadID    string `json:"sender_thread_id"`
	SenderAccountRef  string `json:"sender_account_ref"`
	SellerHoldingID   string `json:"seller_holding_id"`
	Quantity          int32  `json:"quantity"`
	PricePerUnit      string `json:"price_per_unit"`
	Premium           string `json:"premium"`
	SettlementDate    string `json:"settlement_date"`
}

type offerResponse struct {
	RemoteThreadID    string `json:"remote_thread_id"`
	RemoteDisplayName string `json:"remote_display_name"`
	RemoteAccountRef  string `json:"remote_account_ref"`
}

type counterRequest struct {
	SenderBankCode string `json:"sender_bank_code"`
	SenderThreadID string `json:"sender_thread_id"`
	Quantity       int32  `json:"quantity"`
	PricePerUnit   string `json:"price_per_unit"`
	Premium        string `json:"premium"`
	SettlementDate string `json:"settlement_date"`
}

type actionRequest struct {
	SenderBankCode string `json:"sender_bank_code"`
	SenderThreadID string `json:"sender_thread_id"`
}

// ---------------------------------------------------------------------
// In-memory state.
// ---------------------------------------------------------------------

type thread struct {
	ID              string
	SenderBankCode  string
	SenderThreadID  string
	SellerHoldingID string
	Quantity        int32
	PricePerUnit    string
	Premium         string
	SettlementDate  string
	Status          string // "open" | "withdrawn" | "accepted"
	UpdatedAt       time.Time
}

type state struct {
	mu       sync.Mutex
	threads  map[string]*thread
	bankCode string
	seller   string
	holdings []publicHolding
	idCount  atomic.Int64
}

func newState(bankCode string) *state {
	return &state{
		threads:  map[string]*thread{},
		bankCode: bankCode,
		seller:   "mock-seller@partner.local",
		holdings: []publicHolding{
			{
				BankCode:          bankCode,
				SellerUserRef:     "mock-seller@partner.local",
				SellerDisplayName: "Mock Seller (Banka " + bankCode + ")",
				SellerHoldingID:   "mock-holding-aapl-50",
				SecurityTicker:    "AAPL",
				SecurityType:      "stock",
				Currency:          "USD",
				Quantity:          50,
				AskPrice:          "200.00",
				Premium:           "12.00",
			},
		},
	}
}

func (s *state) nextThreadID() string {
	n := s.idCount.Add(1)
	return fmt.Sprintf("mock-thr-%d", n)
}

// ---------------------------------------------------------------------
// HTTP handlers.
// ---------------------------------------------------------------------

func main() {
	port := envOr("PORT", "9099")
	bankCode := envOr("BANK_ROUTING_NUMBER", "999")
	apiKey := envOr("INTERBANK_API_KEY", "dev-outbound-banka3")

	st := newState(bankCode)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /bank/api/v1/otc/public", st.handlePublic)
	mux.HandleFunc("POST /bank/api/v1/otc/external-offers", guard(apiKey, st.handleOffer))
	mux.HandleFunc("POST /bank/api/v1/otc/external-offers/{bank_code}/{thread_id}/counter", guard(apiKey, st.handleCounter))
	mux.HandleFunc("POST /bank/api/v1/otc/external-offers/{bank_code}/{thread_id}/withdraw", guard(apiKey, st.handleAction("withdrawn")))
	mux.HandleFunc("POST /bank/api/v1/otc/external-offers/{bank_code}/{thread_id}/accept", guard(apiKey, st.handleAction("accepted")))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	log.Printf("mock-partner listening on :%s as bank_code=%s", port, bankCode)
	srv := &http.Server{Addr: ":" + port, Handler: logRequests(mux), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("mock-partner listen failed on :%s: %v", port, err)
	}
}

// logRequests logs every inbound request's method + path (healthz
// probes excluded) so interop sessions are debuggable from the mock's
// stdout alone; handlers add the per-route decision lines.
func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			log.Printf("inbound request: method=%s path=%s remote=%s", r.Method, r.URL.Path, r.RemoteAddr)
		}
		h.ServeHTTP(w, r)
	})
}

func (s *state) handlePublic(w http.ResponseWriter, r *http.Request) {
	log.Printf("public feed served: items=%d remote=%s", len(s.holdings), r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"items": s.holdings})
}

func (s *state) handleOffer(w http.ResponseWriter, r *http.Request) {
	var in offerRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		log.Printf("offer rejected: invalid JSON: %v (remote=%s)", err, r.RemoteAddr)
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": 400, "message": "invalid JSON"})
		return
	}
	// Validate seller_holding_id matches our seed.
	if in.SellerHoldingID != "mock-holding-aapl-50" {
		log.Printf("offer rejected: unknown holding=%s sender=%s thread=%s", in.SellerHoldingID, in.SenderBankCode, in.SenderThreadID)
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "unknown holding"})
		return
	}
	s.mu.Lock()
	t := &thread{
		ID:              s.nextThreadID(),
		SenderBankCode:  in.SenderBankCode,
		SenderThreadID:  in.SenderThreadID,
		SellerHoldingID: in.SellerHoldingID,
		Quantity:        in.Quantity,
		PricePerUnit:    in.PricePerUnit,
		Premium:         in.Premium,
		SettlementDate:  in.SettlementDate,
		Status:          "open",
		UpdatedAt:       time.Now(),
	}
	s.threads[t.ID] = t
	s.mu.Unlock()
	log.Printf("offer received: sender=%s thread=%s holding=%s qty=%d", in.SenderBankCode, t.ID, in.SellerHoldingID, in.Quantity)
	writeJSON(w, http.StatusOK, offerResponse{
		RemoteThreadID:    t.ID,
		RemoteDisplayName: s.seller,
		RemoteAccountRef:  "555555555555555555", // 18-digit dummy
	})
}

func (s *state) handleCounter(w http.ResponseWriter, r *http.Request) {
	var in counterRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		log.Printf("counter rejected: invalid JSON: %v (remote=%s)", err, r.RemoteAddr)
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": 400, "message": "invalid JSON"})
		return
	}
	threadID := r.PathValue("thread_id")
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[threadID]
	if !ok {
		log.Printf("counter rejected: thread not found thread=%s sender=%s", threadID, in.SenderBankCode)
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "thread not found"})
		return
	}
	if t.Status != "open" {
		log.Printf("counter rejected: thread not open thread=%s status=%s sender=%s", threadID, t.Status, in.SenderBankCode)
		writeJSON(w, http.StatusConflict, map[string]any{"code": 409, "message": "thread not open"})
		return
	}
	t.Quantity = in.Quantity
	t.PricePerUnit = in.PricePerUnit
	t.Premium = in.Premium
	t.SettlementDate = in.SettlementDate
	t.UpdatedAt = time.Now()
	log.Printf("counter applied: thread=%s qty=%d price=%s premium=%s", threadID, in.Quantity, in.PricePerUnit, in.Premium)
	w.WriteHeader(http.StatusNoContent)
}

func (s *state) handleAction(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in actionRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			log.Printf("%s body ignored: invalid JSON: %v (remote=%s)", target, err, r.RemoteAddr)
		}
		threadID := r.PathValue("thread_id")
		s.mu.Lock()
		defer s.mu.Unlock()
		t, ok := s.threads[threadID]
		if !ok {
			log.Printf("%s rejected: thread not found thread=%s sender=%s", target, threadID, in.SenderBankCode)
			writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "message": "thread not found"})
			return
		}
		if t.Status != "open" {
			log.Printf("%s rejected: thread not open thread=%s status=%s sender=%s", target, threadID, t.Status, in.SenderBankCode)
			writeJSON(w, http.StatusConflict, map[string]any{"code": 409, "message": "thread not open"})
			return
		}
		t.Status = target
		t.UpdatedAt = time.Now()
		log.Printf("thread %s → %s", threadID, target)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------

func guard(apiKey string, h http.HandlerFunc) http.HandlerFunc {
	expected := []byte(apiKey)
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("X-Api-Key"))
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			log.Printf("request rejected: invalid X-Api-Key method=%s path=%s remote=%s", r.Method, r.URL.Path, r.RemoteAddr)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"code": 401, "message": "invalid X-Api-Key"})
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
