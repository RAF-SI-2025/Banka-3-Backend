package cvv

import (
	"errors"
	"testing"
)

func TestHash_DeterministicGivenPepper(t *testing.T) {
	a, err := Hash("123", "pepper-x")
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	b, err := Hash("123", "pepper-x")
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if a != b {
		t.Fatalf("hash should be deterministic for fixed pepper: %q vs %q", a, b)
	}
}

func TestHash_DiffersWithPepper(t *testing.T) {
	a, _ := Hash("123", "p1")
	b, _ := Hash("123", "p2")
	if a == b {
		t.Fatal("hash must depend on pepper")
	}
}

func TestHash_RejectsEmptyPepper(t *testing.T) {
	_, err := Hash("123", "")
	if !errors.Is(err, ErrEmptyPepper) {
		t.Fatalf("want ErrEmptyPepper, got %v", err)
	}
}

func TestVerify(t *testing.T) {
	const pepper = "secret"
	h, err := Hash("742", pepper)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, err := Verify("742", h, pepper)
	if err != nil || !ok {
		t.Fatalf("verify(correct): ok=%v err=%v", ok, err)
	}
	ok, _ = Verify("999", h, pepper)
	if ok {
		t.Fatal("verify(wrong) should be false")
	}
	ok, _ = Verify("742", h, "wrong-pepper")
	if ok {
		t.Fatal("verify(wrong pepper) should be false")
	}
}
