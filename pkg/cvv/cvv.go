// Package cvv hashes a card's verification value (CVV/CVC) with a
// keyed HMAC. Argon2id (which pkg/passwords uses for real passwords)
// is overkill here — the search space is 10^3 = 1000, so any per-guess
// work factor is meaningless against an attacker who has the digest.
// The defensible primitive is HMAC-SHA256 keyed by a server-side
// pepper: stealing the database alone yields nothing because the
// pepper is held in env config, never persisted alongside hashes.
package cvv

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// ErrEmptyPepper guards against an unconfigured pepper. Refusing to
// hash with an empty key forces the operator to set BANK_CVV_PEPPER
// before card creation works in production. Tests can pass a fixed
// value.
var ErrEmptyPepper = errors.New("cvv: pepper is empty; set BANK_CVV_PEPPER")

// Hash returns the hex-encoded HMAC-SHA256 of cvv keyed by pepper.
func Hash(cvv, pepper string) (string, error) {
	if pepper == "" {
		return "", ErrEmptyPepper
	}
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(cvv))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// Verify reports whether cvv hashes to want under the supplied pepper.
// Constant-time comparison avoids leaking partial matches to a timing
// attacker — irrelevant for 3-digit CVVs in practice but cheap and
// correct.
func Verify(cvv, want, pepper string) (bool, error) {
	got, err := Hash(cvv, pepper)
	if err != nil {
		return false, err
	}
	return hmac.Equal([]byte(got), []byte(want)), nil
}
