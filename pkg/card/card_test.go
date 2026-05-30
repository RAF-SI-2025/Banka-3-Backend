package card

import (
	"strings"
	"testing"
)

func TestGenerate_RoundTrip(t *testing.T) {
	for _, b := range []Brand{BrandVisa, BrandMastercard, BrandDinacard, BrandAmex} {
		t.Run(string(b), func(t *testing.T) {
			n, err := Generate(b)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if got, _ := Validate(n); got != b {
				t.Errorf("brand round-trip: want %q, got %q (n=%q)", b, got, n)
			}
			if want := LengthOf(b); len(n) != want {
				t.Errorf("len: want %d, got %d", want, len(n))
			}
		})
	}
}

func TestDetectBrand(t *testing.T) {
	cases := []struct {
		in   string
		want Brand
	}{
		{"4111111111111111", BrandVisa},
		{"5500000000000004", BrandMastercard}, // legacy 51-55
		{"2221000000000009", BrandMastercard}, // newer 2221-2720 range
		{"9891000000000001", BrandDinacard},
		{"340000000000009", BrandAmex},
		{"371000000000000", BrandAmex},
		{"6011000000000004", ""}, // Discover, not supported
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := DetectBrand(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidate_BadInputs(t *testing.T) {
	good, err := Generate(BrandVisa)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"non-digit", "411111111111111A", "non-digit"},
		{"unknown brand", "6011000000000004", "unknown brand"},
		{"wrong length", good[:15], "expects 16 digits"},
		{"luhn flip", flipDigit(good, 4), "Luhn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Validate(tc.input); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestMask(t *testing.T) {
	if got := Mask("4111111111111111"); got != "4111********1111" {
		t.Errorf("mask: %q", got)
	}
	if got := Mask("371000000000000"); got != "3710*******0000" {
		t.Errorf("mask amex: %q (len=%d)", got, len(got))
	}
}

func flipDigit(s string, i int) string {
	b := []byte(s)
	d := (b[i] - '0' + 1) % 10
	b[i] = '0' + d
	return string(b)
}
