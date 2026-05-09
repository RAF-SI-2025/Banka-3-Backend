package account

import (
	"strings"
	"testing"
)

func TestGenerate_RoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bank     string
		branch   string
		typ      Type
		wantType string
	}{
		// Tekući bucket — spec p.16.
		{"personal standard", "333", "0001", TypePersonalChecking, "11"},
		{"business checking", "333", "0001", TypeBusinessChecking, "12"},
		{"savings", "333", "0001", TypeSavings, "13"},
		{"pensioner", "333", "0001", TypePensioner, "14"},
		{"youth", "333", "0001", TypeYouth, "15"},
		{"student", "333", "0001", TypeStudent, "16"},
		{"unemployed", "333", "0001", TypeUnemployed, "17"},
		// Devizni bucket — spec p.16.
		{"personal FX", "333", "0001", TypePersonalFX, "21"},
		{"business FX", "333", "0001", TypeBusinessFX, "22"},
		// System sentinel.
		{"system house account", "333", "0000", TypeSystem, "99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := Generate(tc.bank, tc.branch, tc.typ)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if len(n) != Length {
				t.Fatalf("len: got %d want %d (%q)", len(n), Length, n)
			}
			if !strings.HasPrefix(n, tc.bank) {
				t.Errorf("bank prefix: %q", n)
			}
			if !strings.HasSuffix(n, tc.wantType) {
				t.Errorf("type suffix: want %q, got %q", tc.wantType, n[Length-2:])
			}
			parts, err := Validate(n)
			if err != nil {
				t.Fatalf("Validate fresh number: %v", err)
			}
			if parts.BankCode != tc.bank || parts.Branch != tc.branch || parts.Type != tc.typ {
				t.Errorf("parts mismatch: %+v", parts)
			}
		})
	}
}

func TestGenerate_Uniqueness(t *testing.T) {
	// Crude collision check — 1000 numbers, expect zero collisions.
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		n, err := Generate("333", "0001", TypePersonalChecking)
		if err != nil {
			t.Fatalf("[%d]: %v", i, err)
		}
		if _, dup := seen[n]; dup {
			t.Fatalf("collision at %d: %q", i, n)
		}
		seen[n] = struct{}{}
	}
}

func TestValidate_BadInputs(t *testing.T) {
	good, err := Generate("333", "0001", TypePersonalChecking)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"too short", "12345", "want 18 digits"},
		{"non-digit", "33300001A000000011", "non-digit"},
		{"bad checksum (flip a digit)", flipDigit(good, 5), "checksum mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Validate(tc.input); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestGenerate_RejectsBadBankOrBranch(t *testing.T) {
	if _, err := Generate("33", "0001", TypePersonalChecking); err == nil {
		t.Error("short bank code accepted")
	}
	if _, err := Generate("333", "001", TypePersonalChecking); err == nil {
		t.Error("short branch accepted")
	}
	if _, err := Generate("ABC", "0001", TypePersonalChecking); err == nil {
		t.Error("non-digit bank code accepted")
	}
}

// flipDigit returns s with the digit at index i incremented mod 10 — a
// cheap way to corrupt the checksum without changing the length.
func flipDigit(s string, i int) string {
	b := []byte(s)
	d := (b[i] - '0' + 1) % 10
	b[i] = '0' + d
	return string(b)
}
