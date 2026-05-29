package passwords

import (
	"strings"
	"testing"
)

func TestValidateComplexity(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string // substring; empty = expect nil
	}{
		{"too short (7)", "Aa1aaaa", "between 8 and 32"},
		{"min length 8 ok", "Aa11aaaa", ""},
		{"max length 32 ok", "Aa11" + strings.Repeat("a", 28), ""},
		{"too long (33)", "Aa11" + strings.Repeat("a", 29), "between 8 and 32"},
		{"only one digit", "Aaaaaaa1", "at least 2 digits"},
		{"missing upper", "aa11aaaa", "uppercase"},
		{"missing lower", "AA11AAAA", "lowercase"},
		{"happy path", "Sifra123", ""},
		{"unicode letters don't count as upper/lower", "ČČ11ččča", "uppercase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateComplexity(tc.input)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("want nil, got %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestHashAndVerifyRoundTrip(t *testing.T) {
	const pw = "Sifra123"
	encoded, err := Hash(pw)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("encoded does not look like argon2id PHC: %q", encoded)
	}
	ok, err := Verify(pw, encoded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("Verify returned false for matching password")
	}
}

func TestHashIsNonDeterministic(t *testing.T) {
	a, _ := Hash("Sifra123")
	b, _ := Hash("Sifra123")
	if a == b {
		t.Fatal("two hashes of the same password should not collide (random salt)")
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	encoded, _ := Hash("Sifra123")
	ok, err := Verify("Sifra124", encoded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Fatal("Verify should return false for wrong password")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	cases := []string{
		"",
		"plain text",
		"$argon2id$v=19$m=65536,t=2,p=1$onlytwo",
		"$argon2id$v=19$m=65536,t=2,p=1$YWJj$YWJj$extra",
		"$argon2i$v=19$m=65536,t=2,p=1$YWJj$YWJj", // wrong variant
	}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			if _, err := Verify("any", c); err == nil {
				t.Fatal("Verify should reject malformed hash")
			}
		})
	}
}

func TestVerifyRejectsTamperedHash(t *testing.T) {
	encoded, _ := Hash("Sifra123")
	// Flip the last base64 char of the digest.
	last := encoded[len(encoded)-1]
	swap := byte('A')
	if last == 'A' {
		swap = 'B'
	}
	tampered := encoded[:len(encoded)-1] + string(swap)
	ok, err := Verify("Sifra123", tampered)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Fatal("Verify should return false for tampered digest")
	}
}
