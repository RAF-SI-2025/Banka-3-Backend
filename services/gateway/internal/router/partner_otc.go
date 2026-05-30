// Partner-facing inbound REST surface for celina-5 cross-bank OTC.
//
// These routes are NOT part of /api/v1; partners reach us under
// /bank/api/v1/... so the surface is namespaced separately from the
// user-facing app. Auth is by shared X-Api-Key (INTERBANK_API_KEY);
// the user-facing JWT middleware does not run here.
//
// Wire shape is the "native" protocol — snake_case JSON, settlement
// dates as YYYY-MM-DD strings. Each handler translates into the
// corresponding ExternalOTCService.Receive* gRPC call on trading.

package router

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PartnerOTC holds the deps the partner-facing handlers need. Set on
// the Router when celina-5 is configured (INTERBANK_API_KEY and the
// trading ExternalOTC client are both non-empty); when nil the
// handlers aren't registered.
type PartnerOTC struct {
	APIKey     string
	TradingOTC tradingpb.ExternalOTCServiceClient
	// Trading is used by the partner-facing GET /otc/public route which
	// resolves to TradingService.ListPublicHoldings + native-shape
	// transform. May be nil — when so, the route is registered but
	// returns a 503.
	Trading tradingpb.TradingServiceClient
	// BankRoutingNumber stamps into the response so the partner knows
	// which bank each row came from. Default 333.
	BankRoutingNumber string
}

// partnerOfferRequest mirrors interbank.nativeOfferRequest. We don't
// share the struct (it lives in trading/internal/external/interbank)
// because that's outbound-only; this is the inbound twin.
type partnerOfferRequest struct {
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

type partnerCounterRequest struct {
	SenderBankCode string `json:"sender_bank_code"`
	SenderThreadID string `json:"sender_thread_id"`
	Quantity       int32  `json:"quantity"`
	PricePerUnit   string `json:"price_per_unit"`
	Premium        string `json:"premium"`
	SettlementDate string `json:"settlement_date"`
}

type partnerActionRequest struct {
	SenderBankCode string `json:"sender_bank_code"`
	SenderThreadID string `json:"sender_thread_id"`
}

type partnerExerciseRequest struct {
	SenderBankCode   string `json:"sender_bank_code"`
	SenderContractID string `json:"sender_contract_id"`
	ExerciseOpID     string `json:"exercise_op_id"`
}

type partnerOfferResponse struct {
	RemoteThreadID    string `json:"remote_thread_id"`
	RemoteDisplayName string `json:"remote_display_name"`
	RemoteAccountRef  string `json:"remote_account_ref"`
}

// MountPartnerOTC registers the inbound partner-facing routes on mux.
// No-op when the receiver is nil or under-configured.
func (p *PartnerOTC) MountPartnerOTC(mux *http.ServeMux) {
	if p == nil || p.TradingOTC == nil || p.APIKey == "" {
		return
	}
	// Discovery — partner banks call this to fetch our advertised
	// public holdings. Unauthenticated by spec convention (the data
	// is public-by-definition), so the protocol-detection probe in
	// trading/internal/external/interbank works without sharing the
	// API key first.
	mux.HandleFunc("GET /bank/api/v1/otc/public", p.ListPublic)
	mux.HandleFunc("POST /bank/api/v1/otc/external-offers", p.guard(p.ReceiveOffer))
	mux.HandleFunc("POST /bank/api/v1/otc/external-offers/{bank_code}/{thread_id}/counter", p.guard(p.ReceiveCounter))
	mux.HandleFunc("POST /bank/api/v1/otc/external-offers/{bank_code}/{thread_id}/withdraw", p.guard(p.ReceiveWithdraw))
	mux.HandleFunc("POST /bank/api/v1/otc/external-offers/{bank_code}/{thread_id}/accept", p.guard(p.ReceiveAccept))
	mux.HandleFunc("POST /bank/api/v1/otc/external-contracts/{bank_code}/{contract_id}/exercise", p.guard(p.ReceiveExerciseNotice))
}

// nativePublicHolding is the wire shape that mirrors
// trading/internal/external/interbank/otc.go's outbound expectation —
// the partner discovery code on the OTHER side will decode this
// directly via jsonDecode.
type nativePublicHolding struct {
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

// ListPublic — GET /bank/api/v1/otc/public. Returns rows from
// TradingService.ListPublicHoldings transformed into the native
// protocol's flat shape. No JWT; partners reach this as part of
// protocol detection so it's intentionally unauthenticated.
func (p *PartnerOTC) ListPublic(w http.ResponseWriter, r *http.Request) {
	if p.Trading == nil {
		writeError(w, http.StatusServiceUnavailable, "trading not wired")
		return
	}
	ticker := r.URL.Query().Get("ticker")
	resp, err := p.Trading.ListPublicHoldings(partnerCtx(r.Context()), &tradingpb.ListPublicHoldingsRequest{
		Ticker: ticker,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	bankCode := p.BankRoutingNumber
	if bankCode == "" {
		bankCode = "333"
	}
	items := make([]nativePublicHolding, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, nativePublicHolding{
			BankCode:          bankCode,
			SellerUserRef:     it.GetSellerId(),
			SellerDisplayName: it.GetSellerDisplayName(),
			SellerHoldingID:   it.GetHoldingId(),
			SecurityTicker:    it.GetSecurity().GetTicker(),
			SecurityType:      securityTypeShortString(it.GetSecurity().GetType()),
			Currency:          currencyShortString(it.GetSecurity().GetCurrency()),
			Quantity:          it.GetAvailableCount(),
			AskPrice:          it.GetCurrentPrice(),
			Premium:           "",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func securityTypeShortString(t tradingpb.SecurityType) string {
	switch t {
	case tradingpb.SecurityType_SECURITY_TYPE_STOCK:
		return "stock"
	case tradingpb.SecurityType_SECURITY_TYPE_FUTURE:
		return "future"
	case tradingpb.SecurityType_SECURITY_TYPE_FOREX:
		return "forex"
	case tradingpb.SecurityType_SECURITY_TYPE_OPTION:
		return "option"
	}
	return ""
}

func currencyShortString(c tradingpb.Currency) string {
	switch c {
	case tradingpb.Currency_CURRENCY_RSD:
		return "RSD"
	case tradingpb.Currency_CURRENCY_EUR:
		return "EUR"
	case tradingpb.Currency_CURRENCY_CHF:
		return "CHF"
	case tradingpb.Currency_CURRENCY_USD:
		return "USD"
	case tradingpb.Currency_CURRENCY_GBP:
		return "GBP"
	case tradingpb.Currency_CURRENCY_JPY:
		return "JPY"
	case tradingpb.Currency_CURRENCY_CAD:
		return "CAD"
	case tradingpb.Currency_CURRENCY_AUD:
		return "AUD"
	}
	return ""
}

// guard wraps a partner-facing handler with X-Api-Key auth. Uses a
// constant-time compare to dodge timing side channels.
func (p *PartnerOTC) guard(h http.HandlerFunc) http.HandlerFunc {
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

// ReceiveOffer handles a partner's CreateOffer call.
func (p *PartnerOTC) ReceiveOffer(w http.ResponseWriter, r *http.Request) {
	var in partnerOfferRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	settle, err := parsePartnerDate(in.SettlementDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid settlement_date: "+err.Error())
		return
	}
	out, err := p.TradingOTC.ReceiveExternalOTCOffer(r.Context(), &tradingpb.ReceiveExternalOTCOfferRequest{
		SenderBankCode:    in.SenderBankCode,
		SenderUserRef:     in.SenderUserRef,
		SenderDisplayName: in.SenderDisplayName,
		SenderThreadId:    in.SenderThreadID,
		SellerHoldingId:   in.SellerHoldingID,
		Quantity:          in.Quantity,
		PricePerUnit:      in.PricePerUnit,
		Premium:           in.Premium,
		SettlementDate:    timestamppb.New(settle),
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, partnerOfferResponse{
		RemoteThreadID:    out.GetLocalMirror().GetId(),
		RemoteDisplayName: out.GetLocalMirror().GetLocalUserId(),
		RemoteAccountRef:  out.GetLocalMirror().GetLocalAccountNumber(),
	})
}

// ReceiveCounter handles a partner's counter-offer.
func (p *PartnerOTC) ReceiveCounter(w http.ResponseWriter, r *http.Request) {
	var in partnerCounterRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	settle, err := parsePartnerDate(in.SettlementDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid settlement_date: "+err.Error())
		return
	}
	bankCode := r.PathValue("bank_code")
	threadID := r.PathValue("thread_id")
	if in.SenderBankCode == "" {
		in.SenderBankCode = bankCode
	}
	if in.SenderThreadID == "" {
		in.SenderThreadID = threadID
	}
	_, err = p.TradingOTC.ReceiveExternalOTCCounter(r.Context(), &tradingpb.ReceiveExternalOTCCounterRequest{
		SenderBankCode: in.SenderBankCode,
		SenderThreadId: in.SenderThreadID,
		Quantity:       in.Quantity,
		PricePerUnit:   in.PricePerUnit,
		Premium:        in.Premium,
		SettlementDate: timestamppb.New(settle),
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ReceiveWithdraw / ReceiveAccept share the same envelope.
func (p *PartnerOTC) ReceiveWithdraw(w http.ResponseWriter, r *http.Request) {
	p.dispatchAction(w, r, withdrawAction)
}

func (p *PartnerOTC) ReceiveAccept(w http.ResponseWriter, r *http.Request) {
	p.dispatchAction(w, r, acceptAction)
}

type partnerActionKind int

const (
	withdrawAction partnerActionKind = iota
	acceptAction
)

func (p *PartnerOTC) dispatchAction(w http.ResponseWriter, r *http.Request, kind partnerActionKind) {
	var in partnerActionRequest
	// Bodies may be empty for action endpoints; ignore decode errors
	// when body length is 0.
	_ = json.NewDecoder(r.Body).Decode(&in)
	bankCode := r.PathValue("bank_code")
	threadID := r.PathValue("thread_id")
	if in.SenderBankCode == "" {
		in.SenderBankCode = bankCode
	}
	if in.SenderThreadID == "" {
		in.SenderThreadID = threadID
	}
	req := &tradingpb.ReceiveExternalOTCActionRequest{
		SenderBankCode: in.SenderBankCode,
		SenderThreadId: in.SenderThreadID,
	}
	var err error
	switch kind {
	case withdrawAction:
		_, err = p.TradingOTC.ReceiveExternalOTCWithdraw(r.Context(), req)
	case acceptAction:
		_, err = p.TradingOTC.ReceiveExternalOTCAccept(r.Context(), req)
	}
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ReceiveExerciseNotice handles a partner's exercise notification.
func (p *PartnerOTC) ReceiveExerciseNotice(w http.ResponseWriter, r *http.Request) {
	var in partnerExerciseRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	bankCode := r.PathValue("bank_code")
	contractID := r.PathValue("contract_id")
	if in.SenderBankCode == "" {
		in.SenderBankCode = bankCode
	}
	if in.SenderContractID == "" {
		in.SenderContractID = contractID
	}
	if in.ExerciseOpID == "" {
		writeError(w, http.StatusBadRequest, "exercise_op_id is required")
		return
	}
	_, err := p.TradingOTC.ReceiveExternalOTCExerciseNotice(r.Context(), &tradingpb.ReceiveExternalOTCExerciseNoticeRequest{
		SenderBankCode:   in.SenderBankCode,
		SenderContractId: in.SenderContractID,
		ExerciseOpId:     in.ExerciseOpID,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parsePartnerDate accepts YYYY-MM-DD or RFC3339 (some partners send
// the latter even though the protocol picks the former).
func parsePartnerDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, &dateParseError{value: s}
}

type dateParseError struct{ value string }

func (e *dateParseError) Error() string {
	return "expected YYYY-MM-DD or RFC3339, got " + e.value
}
