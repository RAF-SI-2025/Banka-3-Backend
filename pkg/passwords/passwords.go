// Package passwords hashes and verifies passwords with argon2id, and
// validates the spec-defined complexity policy.
//
// Encoded hashes follow the PHC string format:
//
//	$argon2id$v=19$m=65536,t=2,p=1$<salt>$<hash>
package passwords

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. OWASP recommends m=65536, t=2, p=1 as a safe
// minimum for interactive logins as of 2024.
const (
	argonMemory      uint32 = 64 * 1024
	argonTime        uint32 = 2
	argonParallelism uint8  = 1
	argonKeyLen      uint32 = 32
	argonSaltLen     int    = 16
)

// MinLen and MaxLen enforce the spec constraint:
// "najmanje 8, a najviše 32 karaktera".
const (
	MinLen = 8
	MaxLen = 32
)

// ErrInvalidHash is returned by Verify when the encoded hash is malformed.
var ErrInvalidHash = errors.New("invalid argon2 hash format")

// Hash returns a PHC-encoded argon2id hash of raw.
func Hash(raw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(raw), salt, argonTime, argonMemory, argonParallelism, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory, argonTime, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify reports whether raw matches encoded, in constant time.
func Verify(raw, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrInvalidHash
	}

	var memory, time uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &parallelism); err != nil {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}

	got := argon2.IDKey([]byte(raw), salt, time, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(got, expected) == 1, nil
}

// ValidateComplexity enforces the spec's password rules:
//
//	8 ≤ len(p) ≤ 32, with ≥2 digits, ≥1 uppercase, ≥1 lowercase letter.
//
// Returns nil on pass, a descriptive error on first failure.
func ValidateComplexity(raw string) error {
	if l := len(raw); l < MinLen || l > MaxLen {
		return fmt.Errorf("password must be between %d and %d characters", MinLen, MaxLen)
	}
	var digits, uppers, lowers int
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r >= 'A' && r <= 'Z':
			uppers++
		case r >= 'a' && r <= 'z':
			lowers++
		}
	}
	if digits < 2 {
		return errors.New("password must contain at least 2 digits")
	}
	if uppers < 1 {
		return errors.New("password must contain at least one uppercase letter")
	}
	if lowers < 1 {
		return errors.New("password must contain at least one lowercase letter")
	}
	return nil
}
