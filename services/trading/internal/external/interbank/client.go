package interbank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
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
	// Banka-4 shape: every endpoint under /interbank
	// (/interbank/public-stock, /interbank/negotiations/…,
	// /interbank/user/{routing}/{id}) and the §2 2PC envelope at
	// POST /interbank. The peer base URL is the partner's root; we append
	// the /interbank/… paths. (Named "banka2" for historical reasons —
	// the shape was first reverse-engineered from a Banka-2 Spring bank.)
	ProtocolBanka2 Protocol = "banka2"
)

// Config carries the partner config + low-level HTTP knobs.
type Config struct {
	// Routes maps partner bank codes to base URLs.
	Routes Routes
	// APIKey is sent as X-Api-Key on every outbound request to a
	// partner. Set from INTERBANK_API_KEY.
	APIKey string
	// OwnRoutingNumber identifies this bank to the partner (echoed in
	// request payloads so partners can populate their remote_bank_code).
	OwnRoutingNumber string
	// HTTPClient — caller may override; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Client is the outbound interbank HTTP adapter. Holds the per-partner
// detected protocol behind a sync.Map so concurrent first-use calls on
// different bank codes don't all probe in parallel.
type Client struct {
	cfg       Config
	log       *slog.Logger
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
	return &Client{cfg: cfg, log: log}
}

// baseURL returns the registered URL for the bank code, or "".
func (c *Client) baseURL(bankCode string) string {
	if c.cfg.Routes == nil {
		return ""
	}
	return c.cfg.Routes[bankCode]
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
// back to /public-stock with the API key. Returns ProtocolUnknown when
// neither comes back 200.
func (c *Client) probe(ctx context.Context, bankCode string) Protocol {
	base := c.baseURL(bankCode)
	if base == "" {
		return ProtocolUnknown
	}
	if c.probeOK(ctx, "GET", base+"/bank/api/v1/otc/public", false) {
		return ProtocolNative
	}
	if c.probeOK(ctx, "GET", base+"/interbank/public-stock", true) {
		return ProtocolBanka2
	}
	return ProtocolUnknown
}

// probeOK fires a single probe with a short timeout. withAPIKey
// controls whether to send X-Api-Key (Banka2 always needs it; native
// public discovery works either way).
func (c *Client) probeOK(ctx context.Context, method, url string, withAPIKey bool) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, method, url, nil)
	if err != nil {
		return false
	}
	if withAPIKey && c.cfg.APIKey != "" {
		req.Header.Set("X-Api-Key", c.cfg.APIKey)
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// doJSON marshals body to JSON, attaches API key, fires the request,
// reads up to ~1 MiB of response into respBody, returns the response
// status code and body (regardless of status — callers decide whether
// 4xx/5xx is an error). Non-2xx responses leave respBody populated so
// the caller can surface the partner's error message.
func (c *Client) doJSON(ctx context.Context, method, url string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
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
	if c.cfg.APIKey != "" {
		req.Header.Set("X-Api-Key", c.cfg.APIKey)
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
