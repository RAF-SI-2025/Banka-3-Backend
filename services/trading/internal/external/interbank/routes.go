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

// BankCodes returns the configured partner bank codes in
// non-deterministic order. Used by Discover to fan out.
func (r Routes) BankCodes() []string {
	codes := make([]string, 0, len(r))
	for code := range r {
		codes = append(codes, code)
	}
	return codes
}
