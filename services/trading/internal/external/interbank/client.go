package interbank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/signature"
)

// Protocol identifies which wire shape a partner bank speaks.
type Protocol string

const (
	// ProtocolUnknown — the partner is configured but neither protocol
	// probe came back 200. Outbound calls return a clear error.
	ProtocolUnknown Protocol = ""
	// ProtocolNative — the partner exposes /bank/api/v1/otc/... routes
	// (the rewrite surface). This is what mock-partner and any team
	// running this codebase will speak.
	ProtocolNative Protocol = "native"
	// ProtocolBanka2 — the partner speaks the canonical si-tx-proto /
	// Banka-4 shape: OTC endpoints at the partner root
	// (/public-stock, /negotiations/…, /user/{routing}/{id}) and the §2
	// 2PC envelope at POST /interbank. The peer base URL is the partner's
	// root; we append /public-stock, /negotiations/… and /interbank.
	// (Named "banka2" for historical reasons — the shape was first
	// reverse-engineered from a Banka-2 Spring bank.)
	ProtocolBanka2 Protocol = "banka2"
)

// Config carries the partner config + low-level HTTP knobs.
type Config struct {
	// Routes maps partner bank codes to base URLs.
	Routes Routes
	// APIKey is the default X-Api-Key on outbound requests, used when a
	// partner has no per-bank key in PartnerKeys. Set from INTERBANK_API_KEY.
	APIKey string
	// PartnerKeys maps a partner bank code to the X-Api-Key that partner
	// expects from us (the key THEY issued). Takes precedence over APIKey
	// for that bank. Set from INTERBANK_PARTNER_KEYS. Lets each peer use a
	// distinct outbound key, independent of our inbound key.
	PartnerKeys map[string]string
	// OwnRoutingNumber identifies this bank to the partner (echoed in
	// request payloads so partners can populate their remote_bank_code).
	OwnRoutingNumber string
	// PresentedRouting maps a partner bank code to the routing number that
	// partner has tied to OUR API key — i.e. how they identify us. Most
	// partners know us by OwnRoutingNumber, but a partner that slotted our
	// key into a pre-existing routing entry expects that number instead and
	// rejects any envelope whose idempotenceKey.routingNumber differs
	// ("mismatches X-Api-Key sender"). Banka-2 registered Banka-3's key under
	// their legacy "265" EXBanka slot, so PresentedRouting["222"]="265". Set
	// from INTERBANK_PRESENTED_ROUTING; absent an entry we present
	// OwnRoutingNumber. Only the Banka-2 dialect consults this — native peers
	// know us by our real routing.
	PresentedRouting map[string]string
	// SignKey is the shared secret for the celina-5 digital-signature
	// primitive (INTERBANK_SIGN_KEY). When non-empty, every outbound
	// request is stamped with X-Timestamp, X-Content-Hash, and
	// X-Signature so the receiving bank can authenticate us and detect
	// tampering/replay. Empty disables signing (dev stack without a key).
	SignKey string
	// HTTPClient — caller may override; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Client is the outbound interbank HTTP adapter. Holds the per-partner
// detected protocol behind a sync.Map so concurrent first-use calls on
// different bank codes don't all probe in parallel.
type Client struct {
	cfg       Config
	log       *slog.Logger
	signer    *signature.Signer
	protocols sync.Map // bankCode -> Protocol (sticky after first probe)
}

// New constructs a Client. cfg.HTTPClient defaults if nil.
func New(cfg Config, log *slog.Logger) *Client {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Client{cfg: cfg, log: log, signer: signature.New(cfg.SignKey)}
}

// baseURL returns the registered URL for the bank code, or "".
func (c *Client) baseURL(bankCode string) string {
	if c.cfg.Routes == nil {
		return ""
	}
	return c.cfg.Routes[bankCode]
}

// apiKeyForURL returns the X-Api-Key to send on an outbound request to the
// given URL. It reverse-matches the URL to a configured route so a
// per-partner key (PartnerKeys) can be used; falls back to the default
// APIKey when the partner has no specific key (or the URL isn't a known
// route). Resolving by URL keeps the low-level request helpers from having
// to thread the bank code through every call site.
func (c *Client) apiKeyForURL(url string) string {
	for code, base := range c.cfg.Routes {
		if base != "" && strings.HasPrefix(url, base) {
			if k := c.cfg.PartnerKeys[code]; k != "" {
				return k
			}
			break
		}
	}
	return c.cfg.APIKey
}

// presentedRouting returns the routing number we must present to the given
// partner in idempotenceKey/transactionId/ForeignBankId — i.e. how that
// partner has registered our API key (see Config.PresentedRouting). Falls
// back to OwnRoutingNumber when the partner has no override.
func (c *Client) presentedRouting(bankCode string) int {
	if v := c.cfg.PresentedRouting[bankCode]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	n, _ := strconv.Atoi(c.cfg.OwnRoutingNumber)
	return n
}

// Protocol returns the cached protocol for a partner, probing it on
// first use. Falls back to ProtocolUnknown when both probes fail.
func (c *Client) Protocol(ctx context.Context, bankCode string) Protocol {
	if v, ok := c.protocols.Load(bankCode); ok {
		return v.(Protocol)
	}
	p := c.probe(ctx, bankCode)
	c.protocols.Store(bankCode, p)
	c.log.Info("partner protocol detected",
		"bank_code", bankCode, "protocol", string(p), "base_url", c.baseURL(bankCode))
	return p
}

// probe tries the native /bank/api/v1/otc/public route first; falls
// back to the canonical root /public-stock with the API key. Returns
// ProtocolUnknown when neither comes back 200.
func (c *Client) probe(ctx context.Context, bankCode string) Protocol {
	base := c.baseURL(bankCode)
	if base == "" {
		return ProtocolUnknown
	}
	if c.probeOK(ctx, "GET", base+"/bank/api/v1/otc/public", false) {
		return ProtocolNative
	}
	if c.probeOK(ctx, "GET", base+"/public-stock", true) {
		return ProtocolBanka2
	}
	return ProtocolUnknown
}

// probeOK fires a single probe with a short timeout. withAPIKey
// controls whether to send X-Api-Key (Banka2 always needs it; native
// public discovery works either way). Accepts the response only when
// it is 2xx AND Content-Type is application/json — some partners host
// their SPA on the same vhost as the API and an unknown path falls
// through to a 200 + HTML catchall, which previously fooled the probe
// into picking the wrong dialect.
func (c *Client) probeOK(ctx context.Context, method, url string, withAPIKey bool) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, method, url, nil)
	if err != nil {
		return false
	}
	if withAPIKey {
		if k := c.apiKeyForURL(url); k != "" {
			req.Header.Set("X-Api-Key", k)
		}
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	return strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json")
}

// signRequest stamps the celina-5 digital-signature headers on an
// outbound request when a sign key is configured. body is the exact
// bytes that will be sent (nil/empty for GET probes). When signing is
// disabled the request is left untouched, so a dev stack without
// INTERBANK_SIGN_KEY interoperates with an unsigned peer.
func (c *Client) signRequest(req *http.Request, body []byte) {
	if c.signer == nil || !c.signer.Enabled() {
		return
	}
	ts := c.signer.Timestamp()
	sig, err := c.signer.Sign(body, ts)
	if err != nil {
		// Enabled() is true so the only error is a programming bug;
		// log and send unsigned rather than dropping the request.
		c.log.Warn("interbank sign failed", "error", err)
		return
	}
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Content-Hash", signature.ContentHash(body))
	req.Header.Set("X-Signature", sig)
}

// doJSON marshals body to JSON, attaches API key, fires the request,
// reads up to ~1 MiB of response into respBody, returns the response
// status code and body (regardless of status — callers decide whether
// 4xx/5xx is an error). Non-2xx responses leave respBody populated so
// the caller can surface the partner's error message.
func (c *Client) doJSON(ctx context.Context, method, url string, body any) (int, []byte, error) {
	var reader io.Reader
	var buf []byte
	if body != nil {
		var err error
		buf, err = json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if k := c.apiKeyForURL(url); k != "" {
		req.Header.Set("X-Api-Key", k)
	}
	c.signRequest(req, buf)
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

// errUnsupportedProtocol is returned when we'd need to speak a protocol
// we haven't implemented yet (Banka2 outbound, currently).
type errUnsupportedProtocol struct {
	bankCode string
	protocol Protocol
}

func (e *errUnsupportedProtocol) Error() string {
	return fmt.Sprintf("partner bank %s speaks unsupported protocol %q", e.bankCode, string(e.protocol))
}

// errUnknownBank is returned when no route is configured for a bank code.
type errUnknownBank struct{ bankCode string }

func (e *errUnknownBank) Error() string { return "unknown partner bank: " + e.bankCode }
