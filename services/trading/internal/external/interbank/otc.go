package interbank

import (
	"context"
	"fmt"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

// =====================================================================
// Wire types — the native protocol.
//
// These mirror the inbound REST shapes the gateway exposes on
// /bank/api/v1/otc/... so any rewrite-running partner can interoperate
// with us byte-for-byte. snake_case JSON to match the rewrite's HTTP
// convention; settlement dates as YYYY-MM-DD strings to dodge the
// proto.Timestamp pitfall (memory: yyyymmdd-proto-timestamp).
// =====================================================================

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

type nativePublicResponse struct {
	Items []nativePublicHolding `json:"items"`
}

type nativeOfferRequest struct {
	SenderBankCode    string `json:"sender_bank_code"`
	SenderUserRef     string `json:"sender_user_ref"`
	SenderDisplayName string `json:"sender_display_name"`
	SenderThreadID    string `json:"sender_thread_id"`
	SenderAccountRef  string `json:"sender_account_ref"`

	SellerHoldingID string `json:"seller_holding_id"`
	Quantity        int32  `json:"quantity"`
	PricePerUnit    string `json:"price_per_unit"`
	Premium         string `json:"premium"`
	SettlementDate  string `json:"settlement_date"`
}

type nativeOfferResponse struct {
	RemoteThreadID    string `json:"remote_thread_id"`
	RemoteDisplayName string `json:"remote_display_name"`
	RemoteAccountRef  string `json:"remote_account_ref"`
}

type nativeCounterRequest struct {
	SenderBankCode string `json:"sender_bank_code"`
	SenderThreadID string `json:"sender_thread_id"`

	Quantity       int32  `json:"quantity"`
	PricePerUnit   string `json:"price_per_unit"`
	Premium        string `json:"premium"`
	SettlementDate string `json:"settlement_date"`
}

type nativeActionRequest struct {
	SenderBankCode string `json:"sender_bank_code"`
	SenderThreadID string `json:"sender_thread_id"`
}

type nativeErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// =====================================================================
// service.PartnerOTC implementation.
// =====================================================================

// Discover fans out across configured partner banks (or hits just one
// when bankCode is set) and returns a normalized list. Native partners
// answer /bank/api/v1/otc/public; Banka2 partners aren't implemented
// yet and are skipped with a logged warning.
func (c *Client) Discover(ctx context.Context, bankCode, tickerFilter string) ([]*service.PartnerHolding, error) {
	var targets []string
	if bankCode != "" {
		if c.baseURL(bankCode) == "" {
			return nil, &errUnknownBank{bankCode: bankCode}
		}
		targets = []string{bankCode}
	} else {
		targets = c.cfg.Routes.BankCodes()
	}

	var out []*service.PartnerHolding
	for _, code := range targets {
		rows, err := c.discoverOne(ctx, code, tickerFilter)
		if err != nil {
			c.log.Warn("partner discover failed",
				"bank_code", code, "err", err.Error())
			continue
		}
		out = append(out, rows...)
	}
	return out, nil
}

func (c *Client) discoverOne(ctx context.Context, bankCode, tickerFilter string) ([]*service.PartnerHolding, error) {
	switch c.Protocol(ctx, bankCode) {
	case ProtocolNative:
		return c.discoverNative(ctx, bankCode, tickerFilter)
	case ProtocolBanka2:
		return c.discoverBanka2(ctx, bankCode, tickerFilter)
	default:
		return nil, &errUnsupportedProtocol{bankCode: bankCode, protocol: ProtocolUnknown}
	}
}

func (c *Client) discoverNative(ctx context.Context, bankCode, tickerFilter string) ([]*service.PartnerHolding, error) {
	url := c.baseURL(bankCode) + "/bank/api/v1/otc/public"
	if tickerFilter != "" {
		url += "?ticker=" + tickerFilter
	}
	status, body, err := c.doJSON(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("partner %s discover: HTTP %d", bankCode, status)
	}
	var parsed nativePublicResponse
	if err := jsonDecode(body, &parsed); err != nil {
		return nil, fmt.Errorf("partner %s discover decode: %w", bankCode, err)
	}
	out := make([]*service.PartnerHolding, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		bc := it.BankCode
		if bc == "" {
			bc = bankCode
		}
		out = append(out, &service.PartnerHolding{
			BankCode:         bc,
			SellerUserRef:    it.SellerUserRef,
			SellerDisplay:    it.SellerDisplayName,
			SellerHoldingRef: it.SellerHoldingID,
			SecurityTicker:   it.SecurityTicker,
			SecurityType:     domain.SecurityType(it.SecurityType),
			Currency:         domain.Currency(it.Currency),
			Quantity:         it.Quantity,
			AskPrice:         it.AskPrice,
			Premium:          it.Premium,
		})
	}
	return out, nil
}

// CreateOffer POSTs a new outbound offer to the partner.
func (c *Client) CreateOffer(ctx context.Context, in service.PartnerCreateOfferInput) (*service.PartnerCreateOfferOutput, error) {
	switch c.Protocol(ctx, in.RemoteBankCode) {
	case ProtocolNative:
		return c.createOfferNative(ctx, in)
	case ProtocolBanka2:
		return c.createOfferBanka2(ctx, in)
	default:
		if c.baseURL(in.RemoteBankCode) == "" {
			return nil, &errUnknownBank{bankCode: in.RemoteBankCode}
		}
		return nil, &errUnsupportedProtocol{bankCode: in.RemoteBankCode, protocol: ProtocolUnknown}
	}
}

func (c *Client) createOfferNative(ctx context.Context, in service.PartnerCreateOfferInput) (*service.PartnerCreateOfferOutput, error) {
	body := nativeOfferRequest{
		SenderBankCode:    c.cfg.OwnRoutingNumber,
		SenderUserRef:     in.LocalUserRef,
		SenderDisplayName: in.LocalDisplayName,
		SenderThreadID:    in.LocalThreadID,
		SenderAccountRef:  in.LocalAccountRef,
		SellerHoldingID:   in.SellerHoldingRef,
		Quantity:          in.Quantity,
		PricePerUnit:      in.PricePerUnit,
		Premium:           in.Premium,
		SettlementDate:    in.SettlementDate.UTC().Format(time.DateOnly),
	}
	url := c.baseURL(in.RemoteBankCode) + "/bank/api/v1/otc/external-offers"
	status, respBody, err := c.doJSON(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, partnerErrorFromBody(in.RemoteBankCode, status, respBody)
	}
	var parsed nativeOfferResponse
	if err := jsonDecode(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("partner %s create offer decode: %w", in.RemoteBankCode, err)
	}
	return &service.PartnerCreateOfferOutput{
		RemoteThreadID:    parsed.RemoteThreadID,
		RemoteUserDisplay: parsed.RemoteDisplayName,
		RemoteAccountRef:  parsed.RemoteAccountRef,
	}, nil
}

// Counter POSTs a counter-offer to the partner.
func (c *Client) Counter(ctx context.Context, in service.PartnerActionInput) error {
	return c.action(ctx, in, "counter")
}

// Withdraw POSTs a withdrawal notice to the partner.
func (c *Client) Withdraw(ctx context.Context, in service.PartnerActionInput) error {
	return c.action(ctx, in, "withdraw")
}

// Accept POSTs an accept notice to the partner. Until BE-5 wires the
// premium 2PC, the partner-side accept handler should idempotently
// stash the request and reply 202; the real premium leg fires later.
func (c *Client) Accept(ctx context.Context, in service.PartnerActionInput) error {
	return c.action(ctx, in, "accept")
}

func (c *Client) action(ctx context.Context, in service.PartnerActionInput, verb string) error {
	switch c.Protocol(ctx, in.RemoteBankCode) {
	case ProtocolNative:
		return c.actionNative(ctx, in, verb)
	case ProtocolBanka2:
		return c.actionBanka2(ctx, in, verb)
	default:
		if c.baseURL(in.RemoteBankCode) == "" {
			return &errUnknownBank{bankCode: in.RemoteBankCode}
		}
		return &errUnsupportedProtocol{bankCode: in.RemoteBankCode, protocol: ProtocolUnknown}
	}
}

func (c *Client) actionNative(ctx context.Context, in service.PartnerActionInput, verb string) error {
	url := fmt.Sprintf("%s/bank/api/v1/otc/external-offers/%s/%s/%s",
		c.baseURL(in.RemoteBankCode), c.cfg.OwnRoutingNumber, in.RemoteThreadID, verb)
	var body any
	if verb == "counter" {
		body = nativeCounterRequest{
			SenderBankCode: c.cfg.OwnRoutingNumber,
			SenderThreadID: in.RemoteThreadID,
			Quantity:       in.Quantity,
			PricePerUnit:   in.PricePerUnit,
			Premium:        in.Premium,
			SettlementDate: in.SettlementDate.UTC().Format(time.DateOnly),
		}
	} else {
		body = nativeActionRequest{
			SenderBankCode: c.cfg.OwnRoutingNumber,
			SenderThreadID: in.RemoteThreadID,
		}
	}
	status, respBody, err := c.doJSON(ctx, "POST", url, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return partnerErrorFromBody(in.RemoteBankCode, status, respBody)
	}
	return nil
}

// partnerErrorFromBody decodes the partner's error envelope when
// present, falls back to a generic message otherwise.
func partnerErrorFromBody(bankCode string, status int, body []byte) error {
	var env nativeErrorBody
	if len(body) > 0 && jsonDecode(body, &env) == nil && env.Message != "" {
		return fmt.Errorf("partner %s HTTP %d: %s", bankCode, status, env.Message)
	}
	return fmt.Errorf("partner %s HTTP %d", bankCode, status)
}

// jsonDecode is a thin wrapper around json.Unmarshal that tolerates an
// empty body (treated as a valid empty object).
func jsonDecode(body []byte, out any) error {
	if len(body) == 0 {
		return nil
	}
	return jsonUnmarshal(body, out)
}
