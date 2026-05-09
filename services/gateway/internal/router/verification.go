package router

import (
	"encoding/json"
	"net/http"
	"time"

	pkgauth "github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/verification"
)

// VerificationRequest is the body the FE posts to start a verification
// round. The action kind tells the issuer which downstream operation
// the code will gate; downstream middleware refuses to consume a code
// minted for a different kind, so the FE must keep these in sync.
type VerificationRequest struct {
	ActionKind string `json:"actionKind"`
}

// VerificationResponse carries the issued id, code, and absolute
// expiry. Until the c5 mobile app lands the code travels back to the
// SPA in this response (the spec p.11 mobile flow has no laptop-side
// way to display the code yet); the FE wraps it in a confirmation
// dialog so the user explicitly acknowledges the action.
type VerificationResponse struct {
	VerificationID string    `json:"verificationId"`
	Code           string    `json:"code"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

// allowedKinds gates which action kinds the FE may issue. Hard-coded
// rather than accepting any string so a typo can't silently mint a
// code that no middleware will ever consume.
var allowedKinds = map[string]verification.ActionKind{
	string(verification.ActionPayment):     verification.ActionPayment,
	string(verification.ActionTransfer):    verification.ActionTransfer,
	string(verification.ActionLimitChange): verification.ActionLimitChange,
	string(verification.ActionCardIssue):   verification.ActionCardIssue,
}

// VerificationHandler returns the POST /api/v1/verification/request
// handler. It is mounted under the auth middleware so the principal
// is already on the context.
func (r *Router) VerificationHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if r.Verifier == nil {
			writeError(w, http.StatusServiceUnavailable, "Verifikacija nije konfigurisana.")
			return
		}
		var body VerificationRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "neispravan zahtev")
			return
		}
		kind, ok := allowedKinds[body.ActionKind]
		if !ok {
			writeError(w, http.StatusBadRequest, "nepoznat tip akcije")
			return
		}
		p, ok := pkgauth.PrincipalFrom(req.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "missing access token")
			return
		}
		id, code, exp, err := r.Verifier.Issue(req.Context(), p.UserID, kind)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "Verifikacija privremeno nedostupna.")
			return
		}
		writeJSON(w, http.StatusOK, VerificationResponse{
			VerificationID: id,
			Code:           code,
			ExpiresAt:      exp,
		})
	}
}
