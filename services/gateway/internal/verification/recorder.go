package verification

import (
	"context"
	"errors"
	"log/slog"
	"time"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/verification"
)

// recordTimeout bounds the best-effort history write so a slow or down
// user service can never stall the verification path itself (spec p.11
// — issuing/consuming a code must keep working even if history doesn't).
const recordTimeout = 2 * time.Second

// RecordingVerifier decorates a verification.Verifier, durably
// persisting each issued request and its terminal outcome via the user
// service so the mobile app (spec p.84 "Stranica Verifikacija") can
// show request history marked successful/unsuccessful.
//
// It deliberately never forwards the 6-digit code to the user service
// — only the request id, owner, action kind and success/fail. History
// writes are strictly advisory: a user-service hiccup logs a warning
// and is otherwise swallowed so the spec p.11 gate is unaffected. The
// type also forwards verification.PendingLister so the gateway's
// pending-codes type-assertion keeps working through the wrapper.
type RecordingVerifier struct {
	Inner verification.Verifier
	Users userpb.UserServiceClient
	Log   *slog.Logger
}

var (
	_ verification.Verifier      = (*RecordingVerifier)(nil)
	_ verification.PendingLister = (*RecordingVerifier)(nil)
	_ verification.Approver      = (*RecordingVerifier)(nil)
	_ verification.Rejecter      = (*RecordingVerifier)(nil)
)

func (r *RecordingVerifier) Issue(ctx context.Context, userID string, kind verification.ActionKind) (string, string, time.Time, error) {
	id, code, exp, err := r.Inner.Issue(ctx, userID, kind)
	if err != nil {
		return id, code, exp, err
	}
	rctx, cancel := context.WithTimeout(ctx, recordTimeout)
	defer cancel()
	if _, rerr := r.Users.RecordVerificationEvent(rctx, &userpb.RecordVerificationEventRequest{
		Id:         id,
		UserId:     userID,
		ActionKind: string(kind),
	}); rerr != nil {
		r.Log.WarnContext(rctx, "verification history: record failed", "err", rerr, "id", id)
	}
	return id, code, exp, nil
}

func (r *RecordingVerifier) Consume(ctx context.Context, id, code string, expectedKind verification.ActionKind) error {
	err := r.Inner.Consume(ctx, id, code, expectedKind)
	// Only terminal outcomes resolve the history row. A merely-wrong
	// code (budget not yet spent) and a mismatch/not-found leave it
	// 'pending' — an exhausted-but-never-consumed row past the code
	// TTL is projected to expired by the history endpoint.
	switch {
	case err == nil:
		r.resolve(ctx, id, true)
	case errors.Is(err, verification.ErrTooMany):
		r.resolve(ctx, id, false)
	}
	return err
}

func (r *RecordingVerifier) resolve(ctx context.Context, id string, success bool) {
	rctx, cancel := context.WithTimeout(ctx, recordTimeout)
	defer cancel()
	if _, err := r.Users.ResolveVerificationEvent(rctx, &userpb.ResolveVerificationEventRequest{
		Id:      id,
		Success: success,
	}); err != nil {
		r.Log.WarnContext(rctx, "verification history: resolve failed", "err", err, "id", id, "success", success)
	}
}

// ListPending forwards to the inner verifier when it supports it (the
// Redis Cache does). A verifier without the capability yields no
// pending codes rather than an error — same contract the gateway's
// pending handler already expects.
func (r *RecordingVerifier) ListPending(ctx context.Context, userID string) ([]verification.Pending, error) {
	lister, ok := r.Inner.(verification.PendingLister)
	if !ok {
		return nil, nil
	}
	return lister.ListPending(ctx, userID)
}

// Approve forwards the quick-approve flag to the inner verifier (the
// Redis Cache implements Approver). A verifier without the capability
// reports ErrNotFound so the gateway surfaces a clean 404/410. Approval
// itself writes no history row — the terminal outcome is recorded by
// ConsumeApproved when the gated request actually lands.
func (r *RecordingVerifier) Approve(ctx context.Context, userID, id string) error {
	ap, ok := r.Inner.(verification.Approver)
	if !ok {
		return verification.ErrNotFound
	}
	return ap.Approve(ctx, userID, id)
}

// Reject forwards to the inner verifier (the Redis Cache implements
// Rejecter) and, on a clean reject, resolves the durable history row to
// unsuccessful — so an ignored request shows up as neuspešno in the
// mobile "Verifikacija" history (spec p.84 mode 2 "Ignore"). A verifier
// without the capability reports ErrNotFound so the gateway surfaces a
// clean 404.
func (r *RecordingVerifier) Reject(ctx context.Context, userID, id string) error {
	rj, ok := r.Inner.(verification.Rejecter)
	if !ok {
		return verification.ErrNotFound
	}
	err := rj.Reject(ctx, userID, id)
	if err == nil {
		r.resolve(ctx, id, false)
	}
	return err
}

// ConsumeApproved forwards to the inner verifier and, on a clean
// one-shot consume, resolves the durable history row to success —
// mirroring Consume so a quick-approved action shows up as a successful
// verification in the mobile "Verifikacija" history (spec p.84).
func (r *RecordingVerifier) ConsumeApproved(ctx context.Context, userID, id string, expectedKind verification.ActionKind) error {
	ap, ok := r.Inner.(verification.Approver)
	if !ok {
		return verification.ErrNotFound
	}
	err := ap.ConsumeApproved(ctx, userID, id, expectedKind)
	if err == nil {
		r.resolve(ctx, id, true)
	}
	return err
}
