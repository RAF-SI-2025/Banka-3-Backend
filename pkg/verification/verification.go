// Package verification implements the spec p.11 "Verifikacioni kod"
// flow as a Redis-backed primitive. The web app still gets the code in
// the HTTP response body so the SPA can render it inline (the fake-QR
// dialog exercises the round-trip end-to-end). The mobile app (spec
// p.84) instead polls ListPending and shows the code on the phone;
// that path is purely additive — the web in-body code is unchanged.
//
// Lifecycle:
//
//  1. Issue(userID, actionKind) → (id, code). Stored in Redis with
//     TTL = 5 min and attempts = 0.
//  2. Consume(id, code, expectedActionKind) →
//     - success: deletes the record and returns nil (one-shot).
//     - wrong code: increments attempts; returns ErrWrongCode. After
//     MaxAttempts the record is deleted and ErrTooMany returns until
//     a fresh code is issued.
//     - expired/missing: ErrNotFound.
//     - actionKind mismatch: ErrMismatch (caller asked to consume a
//     payment code on a card-issue endpoint, etc).
//
// Action kind is part of the record so a code minted for one operation
// can't be replayed against another. The caller is responsible for
// passing the right kind on the consume side.
package verification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// CodeTTL is the validity window of a freshly issued verification code.
// Spec p.11 — "kod traje 5 min".
const CodeTTL = 5 * time.Minute

// MaxAttempts is the per-code wrong-code budget before the record is
// destroyed. Spec p.11 — "nakon 3 neuspešna pokušaja se otkazuje
// transakcija".
const MaxAttempts = 3

// CodeLength is the fixed digit count of issued codes. Six digits
// matches the standard 2FA / OTP format and what the FE input expects.
const CodeLength = 6

// ActionKind groups verification records by the operation they gate.
// Add a new constant when wiring a new flow; the consume call must
// pass the same value.
type ActionKind string

const (
	ActionPayment     ActionKind = "payment"
	ActionTransfer    ActionKind = "transfer"
	ActionLimitChange ActionKind = "limit_change"
	ActionCardIssue   ActionKind = "card_issue"

	// c4 — every money-moving OTC + fund flow goes through the same
	// 6-digit verifikacioni-kod gate the client uses for c2 payments.
	// Kept as distinct kinds (rather than one umbrella) so a code
	// minted for "OTC accept" can't be replayed to confirm a fund
	// withdrawal, and the FE renders the right Serbian copy per flow.
	ActionOTCAccept    ActionKind = "otc_accept"
	ActionOTCExercise  ActionKind = "otc_exercise"
	ActionFundInvest   ActionKind = "fund_invest"
	ActionFundWithdraw ActionKind = "fund_withdraw"

	// c5 — cross-bank OTC. Separate kinds (vs reusing
	// ActionOTCAccept / ActionOTCExercise) so a code minted for an
	// intra-bank operation can't satisfy a cross-bank one and vice
	// versa; the FE labels the dialog with the right Serbian copy
	// ("Eksterno prihvatanje", "Eksterno izvršenje").
	ActionExternalOTCAccept   ActionKind = "external_otc_accept"
	ActionExternalOTCExercise ActionKind = "external_otc_exercise"

	// c5 — user-initiated cross-bank cash payment. Distinct from
	// ActionPayment (intra-bank) so an intercepted code from the
	// in-bank payment dialog can't be replayed against the cross-bank
	// route. Same 6-digit dialog UX; different Serbian label
	// ("Međubankarsko plaćanje").
	ActionInterbankPayment ActionKind = "interbank_payment"
)

var (
	// ErrNotFound covers both "never issued" and "expired or already
	// consumed". The two cases are indistinguishable on purpose —
	// callers shouldn't be able to probe Redis for code lifetimes.
	ErrNotFound = errors.New("verification: not found or expired")
	// ErrWrongCode means the supplied code didn't match. Caller may
	// retry up to MaxAttempts times with the same id.
	ErrWrongCode = errors.New("verification: wrong code")
	// ErrTooMany signals the record has been retired due to attempt
	// budget exhaustion. Caller must request a fresh code.
	ErrTooMany = errors.New("verification: too many attempts")
	// ErrMismatch means the record exists but for a different action
	// kind. Surface 401/403 to the caller — never reveal the kind.
	ErrMismatch = errors.New("verification: action mismatch")
	// ErrNotApproved means a quick-approve consume was attempted against
	// a record that exists and belongs to the user but has not been
	// approved from the mobile app. The caller must either approve it
	// (POST .../approve) or fall back to the 6-digit code path.
	ErrNotApproved = errors.New("verification: not approved")
)

// Verifier is the interface the gateway middleware depends on. The
// Redis-backed implementation lives in this package; tests stub it
// with an in-memory map.
type Verifier interface {
	Issue(ctx context.Context, userID string, kind ActionKind) (id, code string, expiresAt time.Time, err error)
	Consume(ctx context.Context, id, code string, expectedKind ActionKind) error
}

// Approver is the optional quick-approve capability (todoSpec S12). The
// mobile app marks a pending record APPROVED instead of relaying the
// 6-digit code; the gateway middleware then admits an id-only request
// via ConsumeApproved. Kept separate from Verifier so existing test
// stubs don't have to implement it — the gateway type-asserts for it.
type Approver interface {
	// Approve marks the pending record id (which must belong to userID)
	// as approved, within its existing TTL. Idempotent: re-approving an
	// already-approved record succeeds.
	Approve(ctx context.Context, userID, id string) error
	// ConsumeApproved retires record id iff it exists, belongs to
	// userID, matches expectedKind, and has been approved. Returns
	// ErrNotApproved when the record exists but was never approved.
	ConsumeApproved(ctx context.Context, userID, id string, expectedKind ActionKind) error
}

// Pending is one active (not yet consumed/expired) verification record,
// as seen by the owning user. Used by the mobile app's "Verifikacija"
// screen (spec p.84) — it displays Code and the user types it back on
// the web app (spec Option 1).
type Pending struct {
	ID        string
	Kind      ActionKind
	Code      string
	Attempts  int
	ExpiresAt time.Time
	// Approved reflects the quick-approve flag (todoSpec S12). The web
	// app polls ListPending and, once a record reports Approved, fires
	// the gated request with X-Verification-Id only (no code).
	Approved bool
}

// PendingLister is an optional capability: list a user's active codes.
// Kept separate from Verifier so the existing in-memory test stubs
// don't have to implement it — the gateway type-asserts for it.
type PendingLister interface {
	ListPending(ctx context.Context, userID string) ([]Pending, error)
}

// Cache is the Redis-backed Verifier (and PendingLister, Approver).
type Cache struct {
	R *redis.Client
}

var (
	_ Verifier      = (*Cache)(nil)
	_ PendingLister = (*Cache)(nil)
	_ Approver      = (*Cache)(nil)
)

func key(id string) string      { return "verif:" + id }
func userKey(uid string) string { return "verif:user:" + uid }

type record struct {
	UserID   string     `json:"u"`
	Kind     ActionKind `json:"k"`
	Code     string     `json:"c"`
	Attempts int        `json:"a"`
	// Approved is set by the mobile quick-approve flow (todoSpec S12).
	// Once true, ConsumeApproved retires the record without a code.
	Approved bool `json:"ap,omitempty"`
}

// Issue mints a fresh verification code, stores the record under a new
// id with CodeTTL, and returns the id + code + absolute expiry.
func (c *Cache) Issue(ctx context.Context, userID string, kind ActionKind) (string, string, time.Time, error) {
	id, err := newID()
	if err != nil {
		return "", "", time.Time{}, err
	}
	code, err := newCode()
	if err != nil {
		return "", "", time.Time{}, err
	}
	rec := record{UserID: userID, Kind: kind, Code: code}
	raw, err := json.Marshal(rec)
	if err != nil {
		return "", "", time.Time{}, err
	}
	expiresAt := time.Now().Add(CodeTTL)
	if err := c.R.Set(ctx, key(id), raw, CodeTTL).Err(); err != nil {
		return "", "", time.Time{}, err
	}
	// Secondary per-user index for the mobile pending viewer (spec
	// p.84). Best-effort: a failed index write must never fail code
	// issuance — the web verification flow doesn't depend on it, and
	// ListPending prunes stale members lazily anyway.
	idx := userKey(userID)
	_ = c.R.ZAdd(ctx, idx, redis.Z{Score: float64(expiresAt.Unix()), Member: id}).Err()
	_ = c.R.Expire(ctx, idx, CodeTTL).Err()
	return id, code, expiresAt, nil
}

// ListPending returns the caller's active verification records. It
// prunes index entries that have expired or whose record was already
// consumed (the record key is gone but the index member lingered).
func (c *Cache) ListPending(ctx context.Context, userID string) ([]Pending, error) {
	idx := userKey(userID)
	now := time.Now().Unix()
	// Drop index members whose expiry score is in the past.
	_ = c.R.ZRemRangeByScore(ctx, idx, "0", strconv.FormatInt(now-1, 10)).Err()

	zs, err := c.R.ZRangeByScoreWithScores(ctx, idx, &redis.ZRangeBy{
		Min: strconv.FormatInt(now, 10),
		Max: "+inf",
	}).Result()
	if err != nil {
		return nil, err
	}

	out := make([]Pending, 0, len(zs))
	for _, z := range zs {
		id, ok := z.Member.(string)
		if !ok || id == "" {
			continue
		}
		raw, gerr := c.R.Get(ctx, key(id)).Bytes()
		if errors.Is(gerr, redis.Nil) {
			// Consumed or expired between the index write and now.
			_ = c.R.ZRem(ctx, idx, id).Err()
			continue
		}
		if gerr != nil {
			return nil, gerr
		}
		var rec record
		if json.Unmarshal(raw, &rec) != nil {
			continue
		}
		out = append(out, Pending{
			ID:        id,
			Kind:      rec.Kind,
			Code:      rec.Code,
			Attempts:  rec.Attempts,
			ExpiresAt: time.Unix(int64(z.Score), 0),
			Approved:  rec.Approved,
		})
	}
	return out, nil
}

// Consume validates id+code against the stored record. On a clean
// match, deletes the record (one-shot). On a wrong code, increments
// the attempt counter; once MaxAttempts is hit the record is deleted
// and subsequent calls return ErrNotFound.
func (c *Cache) Consume(ctx context.Context, id, code string, expectedKind ActionKind) error {
	raw, err := c.R.Get(ctx, key(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return err
	}
	if rec.Kind != expectedKind {
		return ErrMismatch
	}
	if rec.Code == code {
		// One-shot: a successful consume retires the record.
		_ = c.R.Del(ctx, key(id)).Err()
		return nil
	}
	rec.Attempts++
	if rec.Attempts >= MaxAttempts {
		_ = c.R.Del(ctx, key(id)).Err()
		return ErrTooMany
	}
	// Persist incremented attempts under the remaining TTL — the spec's
	// 5-minute window covers the entire 3-attempt budget, so no fresh
	// expiry per attempt.
	ttl, terr := c.R.TTL(ctx, key(id)).Result()
	if terr != nil || ttl <= 0 {
		ttl = CodeTTL
	}
	updated, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := c.R.Set(ctx, key(id), updated, ttl).Err(); err != nil {
		return err
	}
	return ErrWrongCode
}

// Approve marks the record id as approved (todoSpec S12 quick-approve).
// The record must exist and belong to userID; otherwise ErrNotFound.
// The approved flag is persisted under the record's remaining TTL so
// the 5-minute window is unchanged. Idempotent.
func (c *Cache) Approve(ctx context.Context, userID, id string) error {
	raw, err := c.R.Get(ctx, key(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return err
	}
	// Ownership check: a user may only approve their own record. Return
	// ErrNotFound (not a distinct error) so a caller can't probe whether
	// an id belongs to someone else.
	if rec.UserID != userID {
		return ErrNotFound
	}
	if rec.Approved {
		return nil
	}
	rec.Approved = true
	ttl, terr := c.R.TTL(ctx, key(id)).Result()
	if terr != nil || ttl <= 0 {
		ttl = CodeTTL
	}
	updated, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return c.R.Set(ctx, key(id), updated, ttl).Err()
}

// ConsumeApproved retires record id without a code, succeeding only if
// the record exists, belongs to userID, matches expectedKind, and has
// been approved via Approve. Mirrors Consume's one-shot semantics: a
// successful call deletes the record. An existing-but-unapproved record
// returns ErrNotApproved and is left intact (the user may still approve
// it from the phone or fall back to the code path).
func (c *Cache) ConsumeApproved(ctx context.Context, userID, id string, expectedKind ActionKind) error {
	raw, err := c.R.Get(ctx, key(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return err
	}
	if rec.UserID != userID {
		return ErrNotFound
	}
	if rec.Kind != expectedKind {
		return ErrMismatch
	}
	if !rec.Approved {
		return ErrNotApproved
	}
	// One-shot: a successful quick-approve consume retires the record.
	_ = c.R.Del(ctx, key(id)).Err()
	return nil
}

func newID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func newCode() (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(CodeLength), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", CodeLength, n.Int64()), nil
}
