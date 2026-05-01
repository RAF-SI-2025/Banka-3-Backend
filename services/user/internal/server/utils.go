package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/model"
)

// all **shared** utilties go here

func generateSalt() ([]byte, error) {
	salt := make([]byte, 16)
	_, err := rand.Read(salt)
	if err != nil {
		return nil, err
	}
	return salt, nil
}

func HashPassword(password string, salt []byte) []byte {
	hashed := sha256.New()
	hashed.Write(salt)
	hashed.Write([]byte(password))
	return hashed.Sum(nil)
}

func NamesToSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func generateOpaqueToken() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func HashValue(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func buildActionLink(baseURL string, token string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return "", fmt.Errorf("base URL is empty")
	}

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing base URL: %w", err)
	}

	query := parsedURL.Query()
	query.Set("token", token)
	parsedURL.RawQuery = query.Encode()

	return parsedURL.String(), nil
}

// permissionSet converts a list of Permission rows to a string set for easy membership tests.
func permissionSet(perms []model.Permission) map[string]struct{} {
	out := make(map[string]struct{}, len(perms))
	for _, p := range perms {
		out[p.Name] = struct{}{}
	}
	return out
}

// TogglesTradingRole reports whether the `agent` or `supervisor` membership differs
// between the old and new permission sets.
func TogglesTradingRole(oldSet, newSet map[string]struct{}) bool {
	for _, perm := range []string{"agent", "supervisor"} {
		_, inOld := oldSet[perm]
		_, inNew := newSet[perm]
		if inOld != inNew {
			return true
		}
	}
	return false
}

// EnsureAdminImpliesSupervisor returns perms with "supervisor" appended when
// "admin" is present but "supervisor" is not (spec p.38: admin is-a supervisor).
// Idempotent: calling twice yields the same result.
func EnsureAdminImpliesSupervisor(perms []string) []string {
	set := NamesToSet(perms)
	if _, hasAdmin := set["admin"]; !hasAdmin {
		return perms
	}
	if _, hasSup := set["supervisor"]; hasSup {
		return perms
	}
	return append(perms, "supervisor")
}
