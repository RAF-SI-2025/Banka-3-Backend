// Package verification implements the spec p.11 "Verifikacioni kod"
// flow as a Redis-backed primitive. The mobile app is deferred until
// celina 5; until then, requesting a code returns the code in the
// HTTP response body so the SPA can render it inline (the FE wraps
// it in a fake-QR dialog so the round-trip is exercised end-to-end).
//
// Lifecycle:
//
//  1. Issue(userID, actionKind) → (id, code). Stored in Redis with
//     TTL = 5 min and attempts = 0.
//  2. Consume(id, code, expectedActionKind) →
//     - success: deletes the record and returns nil (one-shot).
//     - wrong code: increments attempts; returns ErrWrongCode. After
//       MaxAttempts the record is deleted and ErrTooMany returns until
//       a fresh code is issued.
//     - expired/missing: ErrNotFound.
//     - actionKind mismatch: ErrMismatch (caller asked to consume a
//       payment code on a card-issue endpoint, etc).
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
)

// Verifier is the interface the gateway middleware depends on. The
// Redis-backed implementation lives in this package; tests stub it
// with an in-memory map.
type Verifier interface {
	Issue(ctx context.Context, userID string, kind ActionKind) (id, code string, expiresAt time.Time, err error)
	Consume(ctx context.Context, id, code string, expectedKind ActionKind) error
}

// Cache is the Redis-backed Verifier.
type Cache struct {
	R *redis.Client
}

func key(id string) string { return "verif:" + id }

type record struct {
	UserID   string     `json:"u"`
	Kind     ActionKind `json:"k"`
	Code     string     `json:"c"`
	Attempts int        `json:"a"`
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
	return id, code, expiresAt, nil
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
