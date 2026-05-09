package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	pkgauth "github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/email"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/verification"
)

// VerificationRequest is the body the FE posts to start a verification
// round. The action kind tells the issuer which downstream operation
// the code will gate; downstream middleware refuses to consume a code
// minted for a different kind, so the FE must keep these in sync.
type VerificationRequest struct {
	ActionKind string `json:"actionKind"`
}

// VerificationResponse carries the issued id and absolute expiry. For
// most actions the code is delivered back in the response body so the
// FE can display it inline (mobile app is c5). For card issuance the
// code is emailed to the requester instead, and the Code field is
// blanked out so the FE renders an "check your email" UX.
type VerificationResponse struct {
	VerificationID string    `json:"verificationId"`
	Code           string    `json:"code"`
	ExpiresAt      time.Time `json:"expiresAt"`
	Delivery       string    `json:"delivery"`
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

		resp := VerificationResponse{
			VerificationID: id,
			Code:           code,
			ExpiresAt:      exp,
			Delivery:       "inline",
		}

		// Card issuance is delivered by email — the requester sees a
		// "check your email" UX instead of an inline code. Other action
		// kinds keep the c5-mobile-app placeholder for now.
		if kind == verification.ActionCardIssue {
			to, lookupErr := r.lookupEmail(req.Context(), p)
			if lookupErr != nil || to == "" {
				writeError(w, http.StatusServiceUnavailable, "Nije moguće pronaći email adresu primaoca.")
				return
			}
			if err := r.Mailer.Send(req.Context(), email.Message{
				To:      to,
				Subject: "Verifikacioni kod za izdavanje kartice",
				Body:    cardIssueEmailBody(code),
			}); err != nil {
				writeError(w, http.StatusServiceUnavailable, "Slanje verifikacionog koda nije uspelo.")
				return
			}
			resp.Code = ""
			resp.Delivery = "email"
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// lookupEmail resolves the requester's email by dialing the user
// service. The principal's user kind picks Client vs Employee. Outgoing
// metadata is padded with internal admin permissions because the user
// service requires ClientRead/EmployeeRead — the gateway is trusted
// service-to-service.
func (r *Router) lookupEmail(ctx context.Context, p pkgauth.Principal) (string, error) {
	if r.Users == nil {
		return "", fmt.Errorf("user-service client not configured")
	}
	ctx = pkgauth.AttachToOutgoing(ctx, pkgauth.Principal{
		UserID:      "gateway-internal",
		UserKind:    pkgauth.KindEmployee,
		Permissions: []string{"admin", "client.read", "employee.read"},
	})
	switch p.UserKind {
	case pkgauth.KindClient:
		c, err := r.Users.GetClient(ctx, &userpb.GetClientRequest{Id: p.UserID})
		if err != nil {
			return "", err
		}
		return c.GetEmail(), nil
	case pkgauth.KindEmployee:
		e, err := r.Users.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: p.UserID})
		if err != nil {
			return "", err
		}
		return e.GetEmail(), nil
	}
	return "", fmt.Errorf("unknown user kind: %q", p.UserKind)
}

func cardIssueEmailBody(code string) string {
	return "Poštovani,\n\n" +
		"Vaš verifikacioni kod za izdavanje kartice je: " + code + "\n\n" +
		"Kod važi 5 minuta. Ako niste vi pokrenuli izdavanje kartice, ignorišite ovu poruku.\n\n" +
		"Banka 3"
}
