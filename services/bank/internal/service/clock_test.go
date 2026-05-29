package service

import (
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// TestNow_FallsBackToWallClockWhenUnset proves the helper degrades to
// time.Now when Service.Now hasn't been wired (production path).
func TestNow_FallsBackToWallClockWhenUnset(t *testing.T) {
	svc := &Service{}
	before := time.Now()
	got := svc.now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Errorf("s.now()=%s outside [%s,%s]", got, before, after)
	}
}

// TestNow_HonoursOverride pins the test-injected clock — this is the
// hook every cron / time-stamp path threads through.
func TestNow_HonoursOverride(t *testing.T) {
	pinned := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := &Service{Now: func() time.Time { return pinned }}
	if got := svc.now(); !got.Equal(pinned) {
		t.Errorf("s.now()=%s, want %s", got, pinned)
	}
}

// TestValidateAP_DOBComparedAgainstInjectedClock guards the structural
// path: validateAP reads s.now() rather than time.Now(), so a DOB that
// is in the real-world past but in the *pinned* future fails. Without
// clock injection this test would be impossible.
func TestValidateAP_DOBComparedAgainstInjectedClock(t *testing.T) {
	svc := &Service{Now: func() time.Time { return time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC) }}
	in := CreateAuthorizedPersonInput{
		CompanyID:   "co",
		FirstName:   "A",
		LastName:    "B",
		DateOfBirth: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), // after pinned now
		Gender:      domain.GenderMale,
		Email:       "x@y.z",
		Phone:       "+381601234567",
		Address:     "Street 1",
	}
	if err := svc.validateAP(in); err == nil {
		t.Fatal("expected DOB-in-future rejection against pinned clock")
	}
}
