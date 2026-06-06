// Package signature implements the celina-5 "Digitalni potpis i
// verifikacija" primitive: every message exchanged between banks carries
// a timestamp, a content hash, and a keyed signature so the receiver can
// authenticate the sender and detect tampering or replay.
//
// The scheme is symmetric HMAC-SHA256 over a shared secret (each pair of
// banks agrees on a key out of band). Asymmetric signatures (RSA/Ed25519
// with published public keys) would be the textbook choice, but the
// inter-bank fabric in this project already authenticates peers with a
// shared X-Api-Key; reusing a shared secret for the signature keeps the
// trust model consistent and avoids a key-distribution mechanism the
// spec doesn't call for. The signed value binds the timestamp to the
// payload hash, so a captured signature can't be replayed against a
// different body or (within the skew window) re-sent later.
//
//	signature = base64( HMAC-SHA256( key, ts + "." + hex(sha256(payload)) ) )
//
// Verify recomputes the signature in constant time and rejects any
// timestamp outside the allowed skew window to bound replay.
package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// DefaultSkew is the maximum age (and future drift) tolerated for a
// message timestamp. Messages older than this are rejected as possible
// replays; messages too far in the future are rejected as malformed
// clocks. Five minutes matches the verifikacioni-kod TTL elsewhere in
// the system.
const DefaultSkew = 5 * time.Minute

// Errors returned by Verify. Callers typically only care that
// verification failed (any non-nil error → reject), but the distinct
// values aid logging and tests.
var (
	// ErrNoKey is returned when a Signer has no key configured. Callers
	// that allow an unsigned dev stack should check Enabled() first and
	// skip signing/verification entirely rather than treating this as a
	// rejection.
	ErrNoKey = errors.New("signature: no key configured")
	// ErrBadSignature is returned when the supplied signature does not
	// match the recomputed one (wrong key, tampered payload, or tampered
	// timestamp).
	ErrBadSignature = errors.New("signature: signature mismatch")
	// ErrStaleTimestamp is returned when the timestamp is outside the
	// allowed skew window.
	ErrStaleTimestamp = errors.New("signature: timestamp outside skew window")
	// ErrBadTimestamp is returned when the timestamp can't be parsed.
	ErrBadTimestamp = errors.New("signature: malformed timestamp")
)

// Signer signs and verifies inter-bank messages with a shared secret.
// The zero value (empty key) is a valid "disabled" signer: Enabled()
// reports false and Sign/Verify return ErrNoKey, letting a dev stack
// without INTERBANK_SIGN_KEY run unsigned. Signer is safe for concurrent
// use.
type Signer struct {
	key  []byte
	skew time.Duration
	// now is overridable in tests; nil means time.Now.
	now func() time.Time
}

// New constructs a Signer from a shared-secret key. An empty key yields
// a disabled signer (Enabled()==false). The skew window is DefaultSkew.
func New(key string) *Signer {
	return &Signer{key: []byte(key), skew: DefaultSkew, now: time.Now}
}

// NewWithSkew is New with a caller-chosen skew window. A non-positive
// skew falls back to DefaultSkew.
func NewWithSkew(key string, skew time.Duration) *Signer {
	if skew <= 0 {
		skew = DefaultSkew
	}
	return &Signer{key: []byte(key), skew: skew, now: time.Now}
}

// Enabled reports whether a key is configured. When false, callers
// should neither stamp nor verify signatures (dev mode).
func (s *Signer) Enabled() bool {
	return s != nil && len(s.key) > 0
}

func (s *Signer) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Timestamp returns the current time formatted as a Unix-seconds string,
// suitable for the X-Timestamp header passed to Sign/Verify.
func (s *Signer) Timestamp() string {
	return strconv.FormatInt(s.clock().Unix(), 10)
}

// ContentHash returns the lowercase hex SHA-256 of payload. Exposed so
// callers can stamp an X-Content-Hash header alongside the signature;
// the hash is advisory (Verify recomputes it from the body), but it lets
// a human or a proxy inspect integrity without the key.
func ContentHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// signingString is the value fed to HMAC: "<ts>.<hex sha256(payload)>".
func signingString(payload []byte, ts string) string {
	return ts + "." + ContentHash(payload)
}

// Sign returns the base64 signature over (ts, payload). Returns ErrNoKey
// when the signer is disabled.
func (s *Signer) Sign(payload []byte, ts string) (string, error) {
	if !s.Enabled() {
		return "", ErrNoKey
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(signingString(payload, ts)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify checks sig against (ts, payload) and that ts is within the skew
// window. Returns nil on success, or one of the package errors. The
// signature comparison is constant-time. A disabled signer returns
// ErrNoKey — callers gate on Enabled() before reaching here.
func (s *Signer) Verify(payload []byte, ts, sig string) error {
	if !s.Enabled() {
		return ErrNoKey
	}
	if err := s.checkTimestamp(ts); err != nil {
		return err
	}
	want, err := s.Sign(payload, ts)
	if err != nil {
		return err
	}
	// Compare the decoded bytes so cosmetic base64 differences don't
	// matter; fall back to a string compare if either side isn't valid
	// base64 (a malformed sig simply won't match).
	gotRaw, gErr := base64.StdEncoding.DecodeString(sig)
	wantRaw, wErr := base64.StdEncoding.DecodeString(want)
	if gErr == nil && wErr == nil {
		if subtle.ConstantTimeCompare(gotRaw, wantRaw) == 1 {
			return nil
		}
		return ErrBadSignature
	}
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) == 1 {
		return nil
	}
	return ErrBadSignature
}

// checkTimestamp parses ts (Unix seconds or RFC3339) and rejects it when
// it falls outside [now-skew, now+skew].
func (s *Signer) checkTimestamp(ts string) error {
	t, err := ParseTimestamp(ts)
	if err != nil {
		return err
	}
	delta := s.clock().Sub(t)
	if delta < 0 {
		delta = -delta
	}
	if delta > s.skew {
		return ErrStaleTimestamp
	}
	return nil
}

// ParseTimestamp parses a header timestamp. It accepts Unix seconds
// (what Timestamp emits) and RFC3339 so a partner stamping either form
// interoperates.
func ParseTimestamp(ts string) (time.Time, error) {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return time.Time{}, ErrBadTimestamp
	}
	if secs, err := strconv.ParseInt(ts, 10, 64); err == nil {
		return time.Unix(secs, 0), nil
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t, nil
	}
	return time.Time{}, ErrBadTimestamp
}
