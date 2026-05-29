// Package tokens generates cryptographically random one-time tokens
// (activation, reset, refresh) and computes the storage hash.
//
// The plaintext is what we put in emails and cookies; only the hash
// lives in the database, so a database leak doesn't expose live tokens.
package tokens

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// Generate returns a random URL-safe token of byteLen random bytes plus
// its sha256 hash. Pass the plaintext to the user; persist the hash.
func Generate(byteLen int) (plaintext, hash string, err error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("random: %w", err)
	}
	plaintext = base64.RawURLEncoding.EncodeToString(b)
	hash = Hash(plaintext)
	return plaintext, hash, nil
}

// Hash returns the storage hash for an existing plaintext token. Used
// at lookup time to compare a presented token against persisted hashes.
func Hash(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return base64.RawStdEncoding.EncodeToString(h[:])
}
