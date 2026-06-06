package signature

import (
	"strconv"
	"testing"
	"time"
)

const testKey = "shared-secret-between-banks"

func TestSignVerifyRoundTrip(t *testing.T) {
	s := New(testKey)
	payload := []byte(`{"transaction_id":"abc","amount":"100.00"}`)
	ts := s.Timestamp()

	sig, err := s.Sign(payload, ts)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if sig == "" {
		t.Fatal("Sign returned empty signature")
	}
	if err := s.Verify(payload, ts, sig); err != nil {
		t.Fatalf("Verify of fresh signature failed: %v", err)
	}
}

func TestVerifyTamperedPayloadFails(t *testing.T) {
	s := New(testKey)
	ts := s.Timestamp()
	sig, err := s.Sign([]byte("original body"), ts)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := s.Verify([]byte("tampered body"), ts, sig); err == nil {
		t.Fatal("Verify accepted a tampered payload")
	}
}

func TestVerifyTamperedTimestampFails(t *testing.T) {
	s := New(testKey)
	payload := []byte("body")
	ts := s.Timestamp()
	sig, err := s.Sign(payload, ts)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// A different (still in-window) timestamp must invalidate the sig.
	other := strconv.FormatInt(time.Now().Add(-30*time.Second).Unix(), 10)
	if other == ts {
		other = strconv.FormatInt(time.Now().Add(-60*time.Second).Unix(), 10)
	}
	if err := s.Verify(payload, other, sig); err == nil {
		t.Fatal("Verify accepted a tampered timestamp")
	}
}

func TestVerifyStaleTimestampFails(t *testing.T) {
	s := New(testKey)
	payload := []byte("body")
	// Sign with a timestamp well outside the skew window.
	stale := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	sig, err := s.Sign(payload, stale)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := s.Verify(payload, stale, sig); err != ErrStaleTimestamp {
		t.Fatalf("want ErrStaleTimestamp, got %v", err)
	}
}

func TestVerifyFutureTimestampFails(t *testing.T) {
	s := New(testKey)
	payload := []byte("body")
	future := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
	sig, err := s.Sign(payload, future)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := s.Verify(payload, future, sig); err != ErrStaleTimestamp {
		t.Fatalf("want ErrStaleTimestamp, got %v", err)
	}
}

func TestVerifyWrongKeyFails(t *testing.T) {
	signer := New(testKey)
	verifier := New("a-different-secret")
	payload := []byte("body")
	ts := signer.Timestamp()
	sig, err := signer.Sign(payload, ts)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := verifier.Verify(payload, ts, sig); err != ErrBadSignature {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestVerifyMalformedTimestamp(t *testing.T) {
	s := New(testKey)
	if err := s.Verify([]byte("body"), "not-a-time", "AAAA"); err != ErrBadTimestamp {
		t.Fatalf("want ErrBadTimestamp, got %v", err)
	}
}

func TestVerifyGarbageSignature(t *testing.T) {
	s := New(testKey)
	ts := s.Timestamp()
	if err := s.Verify([]byte("body"), ts, "!!!not-base64!!!"); err == nil {
		t.Fatal("Verify accepted a garbage signature")
	}
}

func TestDisabledSigner(t *testing.T) {
	s := New("")
	if s.Enabled() {
		t.Fatal("empty-key signer reports Enabled")
	}
	if _, err := s.Sign([]byte("x"), "1"); err != ErrNoKey {
		t.Fatalf("want ErrNoKey from Sign, got %v", err)
	}
	if err := s.Verify([]byte("x"), "1", "y"); err != ErrNoKey {
		t.Fatalf("want ErrNoKey from Verify, got %v", err)
	}
}

func TestRFC3339TimestampAccepted(t *testing.T) {
	s := New(testKey)
	payload := []byte("body")
	ts := time.Now().UTC().Format(time.RFC3339)
	sig, err := s.Sign(payload, ts)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := s.Verify(payload, ts, sig); err != nil {
		t.Fatalf("RFC3339 round-trip failed: %v", err)
	}
}

func TestContentHashStable(t *testing.T) {
	h1 := ContentHash([]byte("abc"))
	h2 := ContentHash([]byte("abc"))
	if h1 != h2 {
		t.Fatal("ContentHash not deterministic")
	}
	if h1 == ContentHash([]byte("abd")) {
		t.Fatal("ContentHash collision on different input")
	}
	if len(h1) != 64 {
		t.Fatalf("want 64 hex chars, got %d", len(h1))
	}
}

func TestSkewBoundary(t *testing.T) {
	// A signer whose clock is pinned; verify a timestamp exactly at the
	// edge of the window is accepted and just past it is rejected.
	base := time.Unix(1_700_000_000, 0)
	s := NewWithSkew(testKey, time.Minute)
	s.now = func() time.Time { return base }
	payload := []byte("body")

	atEdge := strconv.FormatInt(base.Add(-time.Minute).Unix(), 10)
	sig, err := s.Sign(payload, atEdge)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := s.Verify(payload, atEdge, sig); err != nil {
		t.Fatalf("edge-of-window timestamp rejected: %v", err)
	}

	past := strconv.FormatInt(base.Add(-time.Minute-time.Second).Unix(), 10)
	sig2, err := s.Sign(payload, past)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := s.Verify(payload, past, sig2); err != ErrStaleTimestamp {
		t.Fatalf("want ErrStaleTimestamp just past window, got %v", err)
	}
}
