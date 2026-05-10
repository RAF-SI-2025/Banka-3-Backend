package service

import (
	"context"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// MarketState is the resolved view of an exchange at a point in time.
type MarketState struct {
	Exchange     *domain.Exchange
	IsOpen       bool
	IsAfterHours bool // within 4h of close per spec p.56
}

// UpsertExchange creates or updates an exchange row. Admin only.
func (s *Service) UpsertExchange(ctx context.Context, in *domain.Exchange) (*domain.Exchange, error) {
	if err := s.requirePermission(ctx, permissions.Admin); err != nil {
		return nil, err
	}
	if err := validateExchange(in); err != nil {
		return nil, err
	}
	return s.Store.UpsertExchange(ctx, in)
}

// SetExchangeOverride writes the four-state override (open / closed /
// after_hours) per spec p.39, plus the after-hours mode for testing
// the spec p.56 cadence path. state==nil clears the override.
// Admin only.
func (s *Service) SetExchangeOverride(ctx context.Context, mic string, state *domain.ExchangeOverrideState) (*domain.Exchange, error) {
	if err := s.requirePermission(ctx, permissions.Admin); err != nil {
		return nil, err
	}
	if mic == "" {
		return nil, apperr.Validation("mic is required")
	}
	if state != nil {
		switch *state {
		case domain.ExchangeOverrideOpen, domain.ExchangeOverrideClosed, domain.ExchangeOverrideAfterHours:
		default:
			return nil, apperr.Validation("invalid override state")
		}
	}
	return s.Store.SetExchangeOverride(ctx, mic, state)
}

// ListExchanges returns every exchange with the resolved is_open /
// after_hours flags. Open to any authenticated user.
func (s *Service) ListExchanges(ctx context.Context) ([]*MarketState, error) {
	if _, err := s.requirePrincipal(ctx); err != nil {
		return nil, err
	}
	es, err := s.Store.ListExchanges(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]*MarketState, 0, len(es))
	for _, e := range es {
		ms := s.resolveMarketState(e, now)
		out = append(out, ms)
	}
	return out, nil
}

// ResolveExchange returns the live state of one exchange or NotFound.
func (s *Service) ResolveExchange(ctx context.Context, mic string) (*MarketState, error) {
	e, err := s.Store.GetExchange(ctx, mic)
	if err != nil {
		return nil, err
	}
	return s.resolveMarketState(e, s.now()), nil
}

// MarketStateForRead resolves the live state from an already-loaded
// exchange row (for the echo path of UpsertExchange / SetOverride
// where we already have the row in memory).
func (s *Service) MarketStateForRead(e *domain.Exchange) *MarketState {
	return s.resolveMarketState(e, s.now())
}

// resolveMarketState computes is_open / is_after_hours from the
// exchange row and the current wall-clock. The override_state column
// short-circuits the schedule entirely.
func (s *Service) resolveMarketState(e *domain.Exchange, now time.Time) *MarketState {
	ms := &MarketState{Exchange: e}
	if e.OverrideState != nil {
		switch *e.OverrideState {
		case domain.ExchangeOverrideOpen:
			ms.IsOpen = true
		case domain.ExchangeOverrideClosed:
			ms.IsOpen = false
		case domain.ExchangeOverrideAfterHours:
			ms.IsOpen = false
			ms.IsAfterHours = true
		}
		return ms
	}
	loc, err := time.LoadLocation(e.Timezone)
	if err != nil {
		loc = time.UTC
	}
	local := now.In(loc)
	openT, err1 := parseHHMM(e.OpenLocal)
	closeT, err2 := parseHHMM(e.CloseLocal)
	if err1 != nil || err2 != nil {
		return ms
	}
	openAt := time.Date(local.Year(), local.Month(), local.Day(), openT.h, openT.m, 0, 0, loc)
	closeAt := time.Date(local.Year(), local.Month(), local.Day(), closeT.h, closeT.m, 0, 0, loc)
	switch local.Weekday() {
	case time.Saturday, time.Sunday:
		// Closed on weekends; spec p.39 doesn't cover holidays — we
		// emit closed and let dev use the override toggle.
		return ms
	}
	switch {
	case local.Before(openAt):
		ms.IsOpen = false
	case local.Before(closeAt):
		ms.IsOpen = true
	default:
		ms.IsOpen = false
		// After-hours = within 4h of close per spec p.56. We compute
		// against local clock so day-of-week / timezone shifts behave
		// correctly.
		if local.Sub(closeAt) <= 4*time.Hour {
			ms.IsAfterHours = true
		}
	}
	return ms
}

type hhmm struct{ h, m int }

func parseHHMM(s string) (hhmm, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return hhmm{}, apperr.Validation("invalid HH:MM: " + s)
	}
	h, err := atoi2(parts[0])
	if err != nil {
		return hhmm{}, err
	}
	m, err := atoi2(parts[1])
	if err != nil {
		return hhmm{}, err
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return hhmm{}, apperr.Validation("HH:MM out of range")
	}
	return hhmm{h: h, m: m}, nil
}

func atoi2(s string) (int, error) {
	if len(s) == 0 || len(s) > 2 {
		return 0, apperr.Validation("expected 1-2 digit number, got " + s)
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, apperr.Validation("expected digits, got " + s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func validateExchange(e *domain.Exchange) error {
	if e.MIC == "" {
		return apperr.Validation("mic is required")
	}
	if e.Name == "" || e.Acronym == "" || e.Polity == "" {
		return apperr.Validation("name, acronym and polity are required")
	}
	if !e.Currency.Supported() {
		return apperr.Validation("unsupported currency")
	}
	if e.Timezone == "" {
		e.Timezone = "UTC"
	}
	if e.OpenLocal == "" {
		e.OpenLocal = "09:30"
	}
	if e.CloseLocal == "" {
		e.CloseLocal = "16:00"
	}
	if _, err := parseHHMM(e.OpenLocal); err != nil {
		return err
	}
	if _, err := parseHHMM(e.CloseLocal); err != nil {
		return err
	}
	return nil
}
