package service

import (
	"errors"
	"testing"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// TestInterbankFailureThreshold pins the spec-derived auto-block
// threshold so a refactor can't silently loosen it.
func TestInterbankFailureThreshold(t *testing.T) {
	if domain.InterbankFailureThreshold != 3 {
		t.Fatalf("threshold = %d, want 3", domain.InterbankFailureThreshold)
	}
}

// TestShouldAutoBlock walks the consecutive-failure counter across the
// threshold boundary: blocks only at/above threshold.
func TestShouldAutoBlock(t *testing.T) {
	cases := []struct {
		failures int
		want     bool
	}{
		{0, false},
		{1, false},
		{2, false},
		{3, true},
		{4, true},
		{10, true},
	}
	for _, tc := range cases {
		if got := shouldAutoBlock(tc.failures); got != tc.want {
			t.Errorf("shouldAutoBlock(%d) = %v, want %v", tc.failures, got, tc.want)
		}
	}
}

// TestIsCountablePrepareFailure: validation / funds / internal errors
// count toward the failure streak; blacklist + already-failed
// (FailedPrecondition) rejections do not.
func TestIsCountablePrepareFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"validation counts", apperr.Validation("bad currency"), true},
		{"not-found counts", apperr.NotFound("source account not found"), true},
		{"internal counts", apperr.Internal("boom", errors.New("x")), true},
		{"blacklisted does not count", apperr.FailedPrecondition("partner bank is blacklisted"), false},
		{"previously-failed does not count", apperr.FailedPrecondition("transaction previously failed"), false},
		{"plain error counts", errors.New("unexpected"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCountablePrepareFailure(tc.err); got != tc.want {
				t.Errorf("isCountablePrepareFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestPageBounds checks the 1-based page → limit/offset normalisation
// including defaults and the upper cap.
func TestPageBounds(t *testing.T) {
	cases := []struct {
		page, size         int
		wantLimit, wantOff int
	}{
		{0, 0, 50, 0},       // defaults
		{1, 20, 20, 0},      // first page
		{3, 20, 20, 40},     // (3-1)*20
		{2, 1000, 200, 200}, // size capped at 200, offset uses cap
		{-5, -5, 50, 0},     // negatives → defaults
	}
	for _, tc := range cases {
		gotLimit, gotOff := pageBounds(tc.page, tc.size)
		if gotLimit != tc.wantLimit || gotOff != tc.wantOff {
			t.Errorf("pageBounds(%d,%d) = (%d,%d), want (%d,%d)",
				tc.page, tc.size, gotLimit, gotOff, tc.wantLimit, tc.wantOff)
		}
	}
}

// TestBlacklistAutoBlockMarker pins the system marker used for
// auto-blocks so the FE can distinguish them from manual blocks.
func TestBlacklistAutoBlockMarker(t *testing.T) {
	if domain.BlacklistAutoBlockBy != "system" {
		t.Fatalf("BlacklistAutoBlockBy = %q, want %q", domain.BlacklistAutoBlockBy, "system")
	}
}

// TestInterbankStatusValues pins the new status strings so they stay in
// lock-step with the migration 0019 CHECK constraint.
func TestInterbankStatusValues(t *testing.T) {
	want := map[domain.InterbankTxStatus]bool{
		"pending":     true,
		"failed":      true,
		"prepared":    true,
		"committed":   true,
		"rolled_back": true,
	}
	for _, s := range []domain.InterbankTxStatus{
		domain.InterbankTxPending, domain.InterbankTxFailed,
		domain.InterbankTxPrepared, domain.InterbankTxCommitted, domain.InterbankTxRolledBack,
	} {
		if !want[s] {
			t.Errorf("status %q not in the migration 0019 CHECK set", s)
		}
	}
}
