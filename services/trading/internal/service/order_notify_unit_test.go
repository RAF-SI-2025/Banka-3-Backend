package service

import (
	"strings"
	"testing"
)

// partialFillBody (S24) must surface both the executed-this-fill count and
// the remaining count.
func TestPartialFillBody_CarriesCounts(t *testing.T) {
	body := partialFillBody(3, 7)
	if !strings.Contains(body, "3") {
		t.Errorf("expected executed qty 3 in body, got %q", body)
	}
	if !strings.Contains(body, "7") {
		t.Errorf("expected remaining qty 7 in body, got %q", body)
	}
}

func TestPartialFillBody_Zeroes(t *testing.T) {
	body := partialFillBody(0, 0)
	if body == "" {
		t.Fatal("expected non-empty body")
	}
	// Both counts render even when zero.
	if strings.Count(body, "0") < 2 {
		t.Errorf("expected both zero counts rendered, got %q", body)
	}
}
