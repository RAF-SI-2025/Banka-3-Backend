package service

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// UpsertActuaryInfoInput carries the supervisor-side promotion of an
// employee to actuary status. The user service is expected to grant
// the matching pkg/permissions Actuary + ActuarySupervisor /
// ActuaryAgent set as a separate step (admin-only); this RPC manages
// only the trading.actuary_info row.
type UpsertActuaryInfoInput struct {
	EmployeeID   string
	Type         domain.ActuaryType
	DailyLimit   string // RSD; ignored for supervisors (forced to "0")
	NeedApproval bool   // ignored for supervisors (forced to false)
}

// UpsertActuaryInfo creates or updates a trading.actuary_info row.
// Supervisors are forced to (limit=0, need_approval=false) per spec
// p.38 ("Supervizor nema limit").
func (s *Service) UpsertActuaryInfo(ctx context.Context, in UpsertActuaryInfoInput) (*domain.ActuaryInfo, error) {
	if _, err := s.requireSupervisor(ctx); err != nil {
		return nil, err
	}
	if in.EmployeeID == "" {
		return nil, apperr.Validation("employee_id is required")
	}
	if in.Type != domain.ActuarySupervisor && in.Type != domain.ActuaryAgent {
		return nil, apperr.Validation("type must be supervisor or agent")
	}

	limit := in.DailyLimit
	need := in.NeedApproval
	if in.Type == domain.ActuarySupervisor {
		limit = "0"
		need = false
	}
	if err := validateNonNegativeAmount(limit); err != nil {
		return nil, err
	}
	// Spec p.38: only supervisors have daily_limit=0 (it means "no
	// cap" — agentNeedsApproval treats 0 as uncapped). An agent with a
	// 0 limit would therefore trade without ever needing approval, so
	// reject it; an agent's limit must be strictly positive.
	if in.Type == domain.ActuaryAgent {
		if err := requirePositiveAgentLimit(limit); err != nil {
			return nil, err
		}
	}

	return s.Store.UpsertActuaryInfo(ctx, &domain.ActuaryInfo{
		EmployeeID:   in.EmployeeID,
		Type:         in.Type,
		DailyLimit:   limit,
		NeedApproval: need,
	})
}

// GetActuaryInfo returns one actuary record. Visibility:
//   - supervisors / admins see anyone;
//   - the actuary themselves can read their own row (so the FE can
//     render their limit on the trading portal).
func (s *Service) GetActuaryInfo(ctx context.Context, employeeID string) (*domain.ActuaryInfo, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if p.UserID != employeeID {
		if _, err := s.requireSupervisor(ctx); err != nil {
			return nil, err
		}
	}
	return s.Store.GetActuaryInfo(ctx, employeeID)
}

// ListActuaries returns the agents (or supervisors / both) the
// supervisor portal needs. Email/name filters are passed through —
// they're not applied here because actuary_info lacks those columns;
// the gateway / FE composes the list with the user service's people
// directory.
func (s *Service) ListActuaries(ctx context.Context, t domain.ActuaryType, page, pageSize int) ([]*domain.ActuaryInfo, int64, error) {
	if _, err := s.requireSupervisor(ctx); err != nil {
		return nil, 0, err
	}
	return s.Store.ListActuaries(ctx, t, page, pageSize)
}

// UpdateActuaryLimit sets the daily limit. Supervisors only; supervisor
// targets are rejected (their limit is meaningless).
func (s *Service) UpdateActuaryLimit(ctx context.Context, employeeID, dailyLimit string) (*domain.ActuaryInfo, error) {
	if _, err := s.requireSupervisor(ctx); err != nil {
		return nil, err
	}
	if err := validateNonNegativeAmount(dailyLimit); err != nil {
		return nil, err
	}
	cur, err := s.Store.GetActuaryInfo(ctx, employeeID)
	if err != nil {
		return nil, err
	}
	if cur.Type == domain.ActuarySupervisor {
		return nil, apperr.FailedPrecondition("supervizor nema limit")
	}
	// Target is an agent here; their limit must stay strictly positive
	// (0 is the supervisor "no cap" sentinel — see UpsertActuaryInfo).
	if err := requirePositiveAgentLimit(dailyLimit); err != nil {
		return nil, err
	}
	// Reject limits below the agent's current used_limit. Otherwise the
	// supervisor can silently put an agent over their cap, which
	// blocks every subsequent order until the cron resets at 23:59.
	newLimit, _ := money.Parse(dailyLimit)
	usedLimit, err := money.Parse(cur.UsedLimit)
	if err != nil {
		return nil, apperr.Validation("used_limit on actuary record is malformed")
	}
	if money.Cmp(newLimit, usedLimit) < 0 {
		return nil, apperr.FailedPrecondition("novi limit ne sme biti manji od trenutno iskorišćenog limita")
	}
	return s.Store.UpdateActuaryLimit(ctx, employeeID, dailyLimit)
}

// ResetActuaryUsedLimit zeroes used_limit for the given agent.
func (s *Service) ResetActuaryUsedLimit(ctx context.Context, employeeID string) (*domain.ActuaryInfo, error) {
	if _, err := s.requireSupervisor(ctx); err != nil {
		return nil, err
	}
	return s.Store.ResetActuaryUsedLimit(ctx, employeeID)
}

// SetActuaryNeedApproval toggles the per-actuary approval requirement.
// Supervisors only; supervisor targets are rejected.
func (s *Service) SetActuaryNeedApproval(ctx context.Context, employeeID string, need bool) (*domain.ActuaryInfo, error) {
	if _, err := s.requireSupervisor(ctx); err != nil {
		return nil, err
	}
	cur, err := s.Store.GetActuaryInfo(ctx, employeeID)
	if err != nil {
		return nil, err
	}
	if cur.Type == domain.ActuarySupervisor {
		return nil, apperr.FailedPrecondition("supervizor uvek ima need_approval=false")
	}
	return s.Store.SetActuaryNeedApproval(ctx, employeeID, need)
}

// RunDailyResetActuaries zeroes used_limit across every actuary. The
// daily 23:59 (Europe/Belgrade) cron calls this; supervisors can call
// it manually via the same RPC for testing / forced reset (spec p.38
// "supervizor ima opciju da resetuje limit i usedLimit za svakog
// agenta, bilo kada").
func (s *Service) RunDailyResetActuaries(ctx context.Context) (int64, error) {
	// The cron path runs without a request principal (no gRPC auth
	// metadata). Allow that explicitly: if there is a principal it
	// must be a supervisor; if there isn't one, accept it as the
	// service-internal call.
	if _, ok := auth.PrincipalFrom(ctx); ok {
		if _, err := s.requireSupervisor(ctx); err != nil {
			return 0, err
		}
	}
	return s.Store.ResetAllUsedLimits(ctx)
}

// validateNonNegativeAmount sanity-checks decimal-string amounts.
func validateNonNegativeAmount(s string) error {
	r, err := money.Parse(s)
	if err != nil {
		return apperr.Validation(err.Error())
	}
	if !money.IsNonNegative(r) {
		return apperr.Validation("amount must be non-negative")
	}
	return nil
}

// requirePositiveAgentLimit enforces spec p.38: an agent's daily limit
// must be strictly positive. 0 is reserved as the supervisor "no cap"
// sentinel (agentNeedsApproval treats 0 as uncapped), so a 0-limit
// agent would never route to supervisor approval.
func requirePositiveAgentLimit(s string) error {
	r, err := money.Parse(s)
	if err != nil {
		return apperr.Validation(err.Error())
	}
	if !money.IsPositive(r) {
		return apperr.Validation("dnevni limit aktuara mora biti veći od 0")
	}
	return nil
}
