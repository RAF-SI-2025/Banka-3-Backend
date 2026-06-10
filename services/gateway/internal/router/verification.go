package router

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	pkgauth "github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/verification"
)

// VerificationRequest is the body the FE posts to start a verification
// round. The action kind tells the issuer which downstream operation
// the code will gate; downstream middleware refuses to consume a code
// minted for a different kind, so the FE must keep these in sync.
type VerificationRequest struct {
	ActionKind string `json:"actionKind"`
}

// VerificationResponse carries the issued id and absolute expiry, plus
// the 6-digit code itself. The mobile app is c5; until then every
// action kind — payments, transfers, limit changes, card issuance —
// returns the code inline so the FE can display it next to a fake-QR
// placeholder.
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
	// c4 — same dialog, distinct kinds so codes don't cross flows.
	string(verification.ActionOTCAccept):    verification.ActionOTCAccept,
	string(verification.ActionOTCExercise):  verification.ActionOTCExercise,
	string(verification.ActionFundInvest):   verification.ActionFundInvest,
	string(verification.ActionFundWithdraw): verification.ActionFundWithdraw,
	// c5 — cross-bank counterparts. Same dialog, distinct kind so the
	// receiver-side middleware can validate the right family.
	string(verification.ActionExternalOTCAccept):   verification.ActionExternalOTCAccept,
	string(verification.ActionExternalOTCExercise): verification.ActionExternalOTCExercise,
	string(verification.ActionInterbankPayment):    verification.ActionInterbankPayment,
}

// actionLabels maps an action kind to Serbian copy the mobile app
// shows next to each pending code (spec p.84 "Verifikacija").
var actionLabels = map[verification.ActionKind]string{
	verification.ActionPayment:             "Plaćanje",
	verification.ActionTransfer:            "Prenos sredstava",
	verification.ActionLimitChange:         "Promena limita",
	verification.ActionCardIssue:           "Izdavanje kartice",
	verification.ActionOTCAccept:           "Prihvatanje OTC ponude",
	verification.ActionOTCExercise:         "Izvršenje opcije",
	verification.ActionFundInvest:          "Ulaganje u fond",
	verification.ActionFundWithdraw:        "Povlačenje iz fonda",
	verification.ActionExternalOTCAccept:   "Prihvatanje OTC ponude (međubankarska)",
	verification.ActionExternalOTCExercise: "Izvršenje opcije (međubankarska)",
	verification.ActionInterbankPayment:    "Međubankarsko plaćanje",
}

func actionLabel(k verification.ActionKind) string {
	if s, ok := actionLabels[k]; ok {
		return s
	}
	return "Verifikacija"
}

// pendingItem is one active code as the mobile app consumes it. Field
// names match the hand-written mobile verification client.
type pendingItem struct {
	ID                string    `json:"id"`
	Action            string    `json:"action"`
	Code              string    `json:"code"`
	ExpiresAt         time.Time `json:"expiresAt"`
	AttemptsRemaining int       `json:"attemptsRemaining"`
	// Approved reflects the quick-approve flag (todoSpec S12): the
	// mobile app hides the "Odobri" button once true, and the web app's
	// poll-mode dialog auto-proceeds with an id-only gated request.
	Approved bool `json:"approved"`
}

type pendingResponse struct {
	Pending []pendingItem `json:"pending"`
}

// VerificationPendingHandler returns GET /api/v1/verification/pending —
// the additive endpoint the mobile app polls (spec p.84, Option 1: the
// phone shows the 6-digit code, the user types it on the web app). It
// is purely additive: the web /verification/request flow and its
// dev-mode in-body code are untouched.
func (r *Router) VerificationPendingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		lister, ok := r.Verifier.(verification.PendingLister)
		if !ok {
			// Verifier without the optional capability (e.g. a stub):
			// no pending codes rather than an error.
			writeJSON(w, http.StatusOK, pendingResponse{Pending: []pendingItem{}})
			return
		}
		p, ok := pkgauth.PrincipalFrom(req.Context())
		if !ok {
			ctx := req.Context()
			logger.From(ctx).WarnContext(ctx, "verification pending rejected: missing principal",
				"path", req.URL.Path)
			writeError(w, http.StatusUnauthorized, "missing access token")
			return
		}
		recs, err := lister.ListPending(req.Context(), p.UserID)
		if err != nil {
			ctx := req.Context()
			logger.From(ctx).ErrorContext(ctx, "verification pending list failed",
				"err", err, "user_id", p.UserID)
			writeError(w, http.StatusServiceUnavailable, "Verifikacija privremeno nedostupna.")
			return
		}
		items := make([]pendingItem, 0, len(recs))
		for _, rec := range recs {
			remaining := verification.MaxAttempts - rec.Attempts
			if remaining < 0 {
				remaining = 0
			}
			items = append(items, pendingItem{
				ID:                rec.ID,
				Action:            actionLabel(rec.Kind),
				Code:              rec.Code,
				ExpiresAt:         rec.ExpiresAt,
				AttemptsRemaining: remaining,
				Approved:          rec.Approved,
			})
		}
		writeJSON(w, http.StatusOK, pendingResponse{Pending: items})
	}
}

// historyItem is one past request as the mobile "Verifikacija" screen
// (spec p.84) consumes it. Field names match the hand-written mobile
// verification client's VerificationHistoryItem.
type historyItem struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Status    string    `json:"status"` // pending | success | failed | expired
	CreatedAt time.Time `json:"createdAt"`
}

type historyResponse struct {
	History []historyItem `json:"history"`
}

// VerificationHistoryHandler returns GET /api/v1/verification/history —
// the durable request history the mobile app shows, each row marked
// successful/unsuccessful (spec p.84 "Stranica Verifikacija"). The
// user service stores only the raw 'pending'|'success'|'failed' state;
// the gateway owns verification timing, so it projects a still-pending
// row older than the code TTL to "expired" here.
func (r *Router) VerificationHistoryHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		p, ok := pkgauth.PrincipalFrom(ctx)
		if !ok {
			logger.From(ctx).WarnContext(ctx, "verification history rejected: missing principal",
				"path", req.URL.Path)
			writeError(w, http.StatusUnauthorized, "missing access token")
			return
		}
		resp, err := r.Users.ListVerificationHistory(req.Context(), &userpb.ListVerificationHistoryRequest{
			UserId: p.UserID,
		})
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "verification history list failed",
				"err", err, "user_id", p.UserID)
			writeError(w, http.StatusServiceUnavailable, "Istorija verifikacija privremeno nedostupna.")
			return
		}
		now := time.Now()
		items := make([]historyItem, 0, len(resp.GetEvents()))
		for _, e := range resp.GetEvents() {
			status := e.GetStatus()
			if status == "pending" && e.GetCreatedAt() != nil &&
				now.Sub(e.GetCreatedAt().AsTime()) > verification.CodeTTL {
				// Issued, never consumed, code window elapsed → an
				// unsuccessful (expired) attempt for display.
				status = "expired"
			}
			items = append(items, historyItem{
				ID:        e.GetId(),
				Action:    actionLabel(verification.ActionKind(e.GetActionKind())),
				Status:    status,
				CreatedAt: e.GetCreatedAt().AsTime(),
			})
		}
		writeJSON(w, http.StatusOK, historyResponse{History: items})
	}
}

// VerificationHandler returns the POST /api/v1/verification/request
// handler. It is mounted under the auth middleware so the principal
// is already on the context.
func (r *Router) VerificationHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		log := logger.From(ctx)
		if r.Verifier == nil {
			log.ErrorContext(ctx, "verification request failed: verifier not configured",
				"path", req.URL.Path)
			writeError(w, http.StatusServiceUnavailable, "Verifikacija nije konfigurisana.")
			return
		}
		var body VerificationRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			log.WarnContext(ctx, "verification request rejected: invalid body", "err", err)
			writeError(w, http.StatusBadRequest, "neispravan zahtev")
			return
		}
		kind, ok := allowedKinds[body.ActionKind]
		if !ok {
			log.WarnContext(ctx, "verification request rejected: unknown action kind",
				"action_kind", body.ActionKind)
			writeError(w, http.StatusBadRequest, "nepoznat tip akcije")
			return
		}
		p, ok := pkgauth.PrincipalFrom(req.Context())
		if !ok {
			log.WarnContext(ctx, "verification request rejected: missing principal",
				"path", req.URL.Path)
			writeError(w, http.StatusUnauthorized, "missing access token")
			return
		}
		id, code, exp, err := r.Verifier.Issue(req.Context(), p.UserID, kind)
		if err != nil {
			// The 6-digit code is never logged, here or anywhere.
			log.ErrorContext(ctx, "verification code issue failed",
				"err", err, "user_id", p.UserID, "action_kind", string(kind))
			writeError(w, http.StatusServiceUnavailable, "Verifikacija privremeno nedostupna.")
			return
		}

		writeJSON(w, http.StatusOK, VerificationResponse{
			VerificationID: id,
			Code:           code,
			ExpiresAt:      exp,
			Delivery:       "inline",
		})
	}
}

// approveResponse confirms a quick-approve (todoSpec S12).
type approveResponse struct {
	ID       string `json:"id"`
	Approved bool   `json:"approved"`
}

// VerificationApproveHandler returns POST /api/v1/verification/{id}/approve
// — the mobile app's quick-approve endpoint (todoSpec S12). Instead of
// relaying the 6-digit code, the client taps "Odobri" and the phone
// marks its own pending record approved in Redis. The next gated
// request the user fires (from the web app) then passes verification
// with X-Verification-Id only. Auth-gated so the caller can only
// approve their own records (ownership enforced in verification.Approve).
func (r *Router) VerificationApproveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		log := logger.From(ctx)
		if r.Verifier == nil {
			log.ErrorContext(ctx, "verification approve failed: verifier not configured",
				"path", req.URL.Path)
			writeError(w, http.StatusServiceUnavailable, "Verifikacija nije konfigurisana.")
			return
		}
		ap, ok := r.Verifier.(verification.Approver)
		if !ok {
			log.ErrorContext(ctx, "verification approve failed: approver not supported",
				"path", req.URL.Path)
			writeError(w, http.StatusServiceUnavailable, "Brzo odobravanje nije dostupno.")
			return
		}
		id := req.PathValue("id")
		if id == "" {
			log.WarnContext(ctx, "verification approve rejected: missing id")
			writeError(w, http.StatusBadRequest, "neispravan zahtev")
			return
		}
		p, ok := pkgauth.PrincipalFrom(req.Context())
		if !ok {
			log.WarnContext(ctx, "verification approve rejected: missing principal",
				"path", req.URL.Path)
			writeError(w, http.StatusUnauthorized, "missing access token")
			return
		}
		err := ap.Approve(req.Context(), p.UserID, id)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, approveResponse{ID: id, Approved: true})
		case errors.Is(err, verification.ErrNotFound):
			// Not owned, expired, or already consumed — indistinguishable
			// on purpose (don't let a caller probe foreign records).
			log.WarnContext(ctx, "verification approve rejected: record not found",
				"verification_id", id, "user_id", p.UserID)
			writeError(w, http.StatusNotFound, "Zahtev za verifikaciju nije pronađen ili je istekao.")
		default:
			log.ErrorContext(ctx, "verification approve failed",
				"err", err, "verification_id", id, "user_id", p.UserID)
			writeError(w, http.StatusServiceUnavailable, "Verifikacija privremeno nedostupna.")
		}
	}
}

// statusResponse reports a single verification record's state to the web
// app's poll-mode dialog (todoSpec S12): pending | approved | expired.
type statusResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// VerificationStatusHandler returns GET /api/v1/verification/{id}/status
// — the web app polls this after opening the quick-approve dialog. It
// reports "approved" once the user taps Odobri on the phone (then the
// dialog fires the gated request id-only), or "expired" when the record
// is gone (consumed elsewhere or TTL elapsed). Derived from the caller's
// own pending list so no foreign record can be probed.
func (r *Router) VerificationStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		id := req.PathValue("id")
		if id == "" {
			logger.From(ctx).WarnContext(ctx, "verification status rejected: missing id")
			writeError(w, http.StatusBadRequest, "neispravan zahtev")
			return
		}
		p, ok := pkgauth.PrincipalFrom(req.Context())
		if !ok {
			logger.From(ctx).WarnContext(ctx, "verification status rejected: missing principal",
				"path", req.URL.Path)
			writeError(w, http.StatusUnauthorized, "missing access token")
			return
		}
		lister, ok := r.Verifier.(verification.PendingLister)
		if !ok {
			writeJSON(w, http.StatusOK, statusResponse{ID: id, Status: "expired"})
			return
		}
		recs, err := lister.ListPending(req.Context(), p.UserID)
		if err != nil {
			ctx := req.Context()
			logger.From(ctx).ErrorContext(ctx, "verification status list failed",
				"err", err, "verification_id", id, "user_id", p.UserID)
			writeError(w, http.StatusServiceUnavailable, "Verifikacija privremeno nedostupna.")
			return
		}
		for _, rec := range recs {
			if rec.ID != id {
				continue
			}
			if rec.Approved {
				writeJSON(w, http.StatusOK, statusResponse{ID: id, Status: "approved"})
				return
			}
			writeJSON(w, http.StatusOK, statusResponse{ID: id, Status: "pending"})
			return
		}
		// Not in the active pending set for this user: consumed or
		// expired. The web dialog treats "expired" as terminal.
		writeJSON(w, http.StatusOK, statusResponse{ID: id, Status: "expired"})
	}
}
