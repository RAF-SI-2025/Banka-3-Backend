package interbank

import "testing"

func TestParsePartnerKeys(t *testing.T) {
	got := ParsePartnerKeys("444:bank4-secret-key, 999:mockkey ,bad,:nokey,777:")
	if len(got) != 2 {
		t.Fatalf("want 2 keys, got %d: %v", len(got), got)
	}
	if got["444"] != "bank4-secret-key" {
		t.Errorf("444: want bank4-secret-key, got %q", got["444"])
	}
	if got["999"] != "mockkey" {
		t.Errorf("999: want mockkey, got %q", got["999"])
	}
	if _, ok := got["777"]; ok {
		t.Errorf("777 had empty key, should be dropped")
	}
}

func TestPresentedRouting(t *testing.T) {
	c := New(Config{
		OwnRoutingNumber: "333",
		// Banka-2 (code 222) registered our key under their legacy 265 slot;
		// 444 has no override and should fall back to our real routing.
		PresentedRouting: ParsePresentedRouting("222:265"),
	}, nil)

	if got := c.presentedRouting("222"); got != 265 {
		t.Errorf("presentedRouting(222) = %d, want 265 (Banka-2 knows us as 265)", got)
	}
	if got := c.presentedRouting("444"); got != 333 {
		t.Errorf("presentedRouting(444) = %d, want 333 (fallback to OwnRoutingNumber)", got)
	}
	if got := c.presentedRouting("999"); got != 333 {
		t.Errorf("presentedRouting(999) = %d, want 333 (no override)", got)
	}
}

func TestAPIKeyForURL(t *testing.T) {
	c := New(Config{
		Routes:      Routes{"444": "http://rafsi.davidovic.io:8083", "999": "http://mock-partner:9099"},
		APIKey:      "default-inbound-key",
		PartnerKeys: map[string]string{"444": "bank4-secret-key"},
	}, nil)

	cases := []struct {
		url, want string
	}{
		// 444 has a per-partner key — used for every path under its base
		// (OTC at root, 2PC envelope under /interbank).
		{"http://rafsi.davidovic.io:8083/public-stock", "bank4-secret-key"},
		{"http://rafsi.davidovic.io:8083/interbank", "bank4-secret-key"},
		// 999 has no per-partner key — falls back to the default.
		{"http://mock-partner:9099/bank/api/v1/otc/public", "default-inbound-key"},
		// Unknown host — falls back to the default.
		{"http://somewhere-else/x", "default-inbound-key"},
	}
	for _, tc := range cases {
		if got := c.apiKeyForURL(tc.url); got != tc.want {
			t.Errorf("apiKeyForURL(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}
