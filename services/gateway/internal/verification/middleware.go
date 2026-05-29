// Package verification adapts pkg/verification to the gateway's HTTP
// pipeline. It mounts in front of routes flagged in `Rules` and
// expects the FE to attach X-Verification-Id + X-Verification-Code
// headers per the spec p.11 flow.
//
// The handler-side route table is the source of truth for which
// action kind a given URL maps to. The FE chooses the same kind when
// issuing the code; if they disagree, Consume returns ErrMismatch and
// the middleware rejects the request.
package verification

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/verification"
)

// Header names the FE attaches on a mutating request that has been
// confirmed via the verification dialog.
const (
	HeaderID   = "X-Verification-Id"
	HeaderCode = "X-Verification-Code"
)

// Rule maps a (method, path-pattern) tuple to the verification kind
// the gated route requires.
type Rule struct {
	Method  string
	Pattern *regexp.Regexp
	Kind    verification.ActionKind
}

// DefaultRules covers the spec p.11 surface (payments, transfers,
// limit changes) plus card issuance per spec p.28, plus the c4
// money-moving OTC + funds surface (spec p.64-76). Internal cron
// endpoints (loans/run-*-job) are not exposed to clients and do not
// need verification — they're employee-triggered and run with
// service credentials.
func DefaultRules() []Rule {
	return []Rule{
		{http.MethodPost, regexp.MustCompile(`^/api/v1/payments$`), verification.ActionPayment},
		{http.MethodPost, regexp.MustCompile(`^/api/v1/transfers$`), verification.ActionTransfer},
		{http.MethodPost, regexp.MustCompile(`^/api/v1/cards$`), verification.ActionCardIssue},
		{http.MethodPatch, regexp.MustCompile(`^/api/v1/accounts/[^/]+/limits$`), verification.ActionLimitChange},
		{http.MethodPatch, regexp.MustCompile(`^/api/v1/cards/[^/]+/limit$`), verification.ActionLimitChange},
		// c4 — money-moving OTC + funds endpoints. The four routes
		// below all transfer real money on behalf of a client; the same
		// 6-digit dialog clients see on c2 payments gates them. Until
		// the c5 mobile app lands the code travels back inline (see
		// pkg/verification.Issue + router/verification.go).
		{http.MethodPost, regexp.MustCompile(`^/api/v1/otc/offers/[^/]+/accept$`), verification.ActionOTCAccept},
		{http.MethodPost, regexp.MustCompile(`^/api/v1/otc/contracts/[^/]+/exercise$`), verification.ActionOTCExercise},
		{http.MethodPost, regexp.MustCompile(`^/api/v1/funds/[^/]+/invest$`), verification.ActionFundInvest},
		{http.MethodPost, regexp.MustCompile(`^/api/v1/funds/[^/]+/withdraw$`), verification.ActionFundWithdraw},
		// c5 — cross-bank money-moving routes mirror the local OTC gate.
		// Same 6-digit dialog; the FE branches the Serbian copy per
		// route (External*). Same shape for the URL — partner bank_code
		// and thread/contract id are path segments.
		{http.MethodPost, regexp.MustCompile(`^/api/v1/otc/external-offers/[^/]+/[^/]+/accept$`), verification.ActionExternalOTCAccept},
		{http.MethodPost, regexp.MustCompile(`^/api/v1/otc/external-contracts/[^/]+/[^/]+/exercise$`), verification.ActionExternalOTCExercise},
		// User-initiated cross-bank cash payment. Distinct action kind
		// from ActionPayment so an intra-bank code can't satisfy this
		// gate (and vice versa); same 6-digit UX, FE labels the dialog
		// "Međubankarsko plaćanje".
		{http.MethodPost, regexp.MustCompile(`^/api/v1/payments/interbank$`), verification.ActionInterbankPayment},
	}
}

// matchRule returns the kind the request must be verified against, or
// "" if the route doesn't require verification.
func matchRule(rules []Rule, method, path string) (verification.ActionKind, bool) {
	for _, r := range rules {
		if r.Method != method {
			continue
		}
		if r.Pattern.MatchString(path) {
			return r.Kind, true
		}
	}
	return "", false
}

// Middleware returns an HTTP middleware that consumes a verification
// code on every request matching the configured rules.
func Middleware(v verification.Verifier, rules []Rule, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			kind, gated := matchRule(rules, r.Method, r.URL.Path)
			if !gated {
				next.ServeHTTP(w, r)
				return
			}
			id := r.Header.Get(HeaderID)
			code := r.Header.Get(HeaderCode)
			if id == "" || code == "" {
				writeErr(w, http.StatusUnauthorized, "Verifikacioni kod je obavezan.")
				return
			}
			err := v.Consume(r.Context(), id, code, kind)
			switch {
			case err == nil:
				next.ServeHTTP(w, r)
			case errors.Is(err, verification.ErrWrongCode):
				writeErr(w, http.StatusUnauthorized, "Pogrešan verifikacioni kod.")
			case errors.Is(err, verification.ErrTooMany):
				writeErr(w, http.StatusUnauthorized, "Previše neuspešnih pokušaja. Zatraži novi kod.")
			case errors.Is(err, verification.ErrNotFound):
				writeErr(w, http.StatusUnauthorized, "Verifikacioni kod je istekao. Zatraži novi.")
			case errors.Is(err, verification.ErrMismatch):
				writeErr(w, http.StatusUnauthorized, "Verifikacioni kod ne odgovara ovoj akciji.")
			default:
				log.Warn("verification consume failed", "error", err)
				writeErr(w, http.StatusServiceUnavailable, "Verifikacija privremeno nedostupna.")
			}
		})
	}
}

type errBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errBody{Code: status, Message: msg})
}
