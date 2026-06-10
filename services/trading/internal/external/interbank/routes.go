// Package interbank is the outbound side of the celina-5 cross-bank
// adapter. It implements service.PartnerOTC against the partner banks
// configured via the INTERBANK_ROUTES env var, probing each partner
// once to decide whether they speak the native protocol (the rewrite
// surface) or the older Banka2 shape (other student teams). The
// inbound (partner-facing) REST endpoints live in the gateway router;
// this package is only for outbound calls.
package interbank

import (
	"strings"
)

// Routes maps a partner bank code (e.g. "444") to its base URL
// (e.g. "https://team4.example.org"). The map is built once at boot
// from the INTERBANK_ROUTES env var and treated as read-only after.
type Routes map[string]string

// ParseRoutes parses an INTERBANK_ROUTES value of the form
// "code:url,code:url,..." into a Routes map. Trailing slashes on URLs
// are trimmed so subsequent path concatenation doesn't double-slash.
// Malformed entries are silently dropped (the bank simply isn't
// reachable; the caller surfaces "unknown partner bank" on first use).
func ParseRoutes(raw string) Routes {
	out := make(Routes)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = strings.TrimRight(value, "/")
	}
	return out
}

// ParsePartnerKeys parses an INTERBANK_PARTNER_KEYS value of the form
// "code:key,code:key,..." into a code→outbound-X-Api-Key map. Each bank
// we call may expect a different key (the key THEY issued to us), distinct
// from the key we validate on inbound (INTERBANK_API_KEY). Keys never
// contain ':' (bank codes are numeric), so a first-':' cut is safe.
// Malformed entries are dropped; the per-route key simply isn't set and
// the client falls back to INTERBANK_API_KEY.
func ParsePartnerKeys(raw string) map[string]string {
	out := make(map[string]string)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		code, key, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		code = strings.TrimSpace(code)
		key = strings.TrimSpace(key)
		if code == "" || key == "" {
			continue
		}
		out[code] = key
	}
	return out
}

// ParsePresentedRouting parses an INTERBANK_PRESENTED_ROUTING value of the
// form "code:routing,code:routing,..." into a partner-code → our-presented-
// routing map. It shares the generic "code:value" grammar with
// ParsePartnerKeys; the distinct name documents intent at the call site.
// Example: "222:265" — present routing 265 to Banka-2 (code 222) because
// that is the routing they tied to our API key.
func ParsePresentedRouting(raw string) map[string]string {
	return ParsePartnerKeys(raw)
}

// BankCodes returns the configured partner bank codes in
// non-deterministic order. Used by Discover to fan out.
func (r Routes) BankCodes() []string {
	codes := make([]string, 0, len(r))
	for code := range r {
		codes = append(codes, code)
	}
	return codes
}
