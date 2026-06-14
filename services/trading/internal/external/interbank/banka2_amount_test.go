package interbank

import "testing"

// Banka-2 serializes share counts on /public-stock as JSON numbers with a
// decimal scale ("9.0000"). A plain ParseInt fails on those, which used to
// drop the quantity to 0 and hide/disable every partner offer on our side.
func TestParseBanka2Amount(t *testing.T) {
	cases := []struct {
		in     string
		want   int32
		wantOK bool
	}{
		{"9.0000", 9, true}, // Banka-2's decimal-scaled form (the bug)
		{"25.0000", 25, true},
		{"0.0000", 0, true},
		{"9", 9, true}, // plain integer still works
		{"100", 100, true},
		{"9.5", 10, true}, // rounded (shares are whole units)
		{"", 0, false},    // genuinely unparseable
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := parseBanka2Amount(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("parseBanka2Amount(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}
