package money

import "testing"

func TestParse_RoundTripFormat(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"100", "100.0000"},
		{"117.5", "117.50000000"},
		{"117.20000000", "117.20000000"},
		{"0", "0.0000"},
		{"", "0.0000"}, // empty → zero
		{"-50.5", "-50.5000"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			r, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.in, err)
			}
			scale := AmountScale
			if tc.want == "117.50000000" || tc.want == "117.20000000" {
				scale = RateScale
			}
			if got := Format(r, scale); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestArithmetic(t *testing.T) {
	a := MustParse("100.50")
	b := MustParse("0.005")
	if got := FormatAmount(Mul(a, b)); got != "0.5025" {
		t.Errorf("100.50 * 0.005 = %s, want 0.5025", got)
	}
	if got := FormatAmount(Sub(a, MustParse("0.50"))); got != "100.0000" {
		t.Errorf("100.50 - 0.50 = %s", got)
	}
	q, err := Div(MustParse("1175"), MustParse("117.50"))
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatAmount(q); got != "10.0000" {
		t.Errorf("1175 / 117.50 = %s, want 10.0000", got)
	}
}

func TestParse_GarbageRejected(t *testing.T) {
	for _, in := range []string{"abc", "1.2.3", "10x"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should error", in)
		}
	}
}

func TestDivByZero(t *testing.T) {
	if _, err := Div(MustParse("1"), MustParse("0")); err == nil {
		t.Error("Div by zero should error")
	}
}
