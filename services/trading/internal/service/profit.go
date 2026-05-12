// Profit Banke dashboards (c4 PR4 — spec p.76).
//
// Two read-only aggregations behind `bank.profit.read`:
//
//   - ListActuaryPerformances — lifetime RSD profit per actuary, sourced
//     from realized_gains where user_kind='employee'. Joins actuary_info
//     so non-actuary employees never surface (only actuaries move bank
//     money).
//   - ListBankFundPositions — the bank's own positions in investment
//     funds (`client_id = BankAsClientOwnerID`), decorated with the
//     fund's name + manager display name.

package service

import (
	"context"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	tdomain "github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/google/uuid"
)

// ListActuaryPerformancesInput narrows the dashboard.
type ListActuaryPerformancesInput struct {
	// Type narrows on actuary_info.type. "" matches both.
	Type tdomain.ActuaryType
	// NameQuery is a case-insensitive substring of "first last", applied
	// after display-name resolution. Falls back to UUID substring when
	// the user-resolver isn't wired (slice-1 tests).
	NameQuery string
}

// ActuaryPerformanceRow is one decorated row returned to the supervisor
// dashboard.
type ActuaryPerformanceRow struct {
	UserID        string
	DisplayName   string
	Type          tdomain.ActuaryType
	ProfitRSD     string
	RealizedCount int64
}

// ListActuaryPerformances returns the leaderboard. Gated by
// `bank.profit.read` (admins implicitly hold it via requireSupervisor's
// admin short-circuit — but we keep the explicit perm check so a
// non-supervisor admin still passes, mirroring the other profit
// dashboards).
func (s *Service) ListActuaryPerformances(ctx context.Context, in ListActuaryPerformancesInput) ([]*ActuaryPerformanceRow, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.HasAny(p.Permissions, permissions.Admin, permissions.BankProfitRead) {
		return nil, apperr.PermissionDenied("nedovoljne permisije za Profit Banke")
	}
	aggs, err := s.Store.ListActuaryPerformances(ctx, string(in.Type))
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(in.NameQuery))
	out := make([]*ActuaryPerformanceRow, 0, len(aggs))
	for _, a := range aggs {
		name := ""
		if s.Users != nil {
			n, err := s.Users.DisplayName(ctx, a.UserID, tdomain.KindEmployee)
			if err != nil {
				// Resolution failure shouldn't drop the row from the
				// supervisor view — log and fall through with an empty
				// name. Same policy as ListTaxPositions.
				s.Log.Warn("resolve actuary display_name failed",
					"user_id", a.UserID, "err", err.Error())
			} else {
				name = n
			}
		}
		if needle != "" {
			haystack := strings.ToLower(name)
			if name == "" {
				haystack = strings.ToLower(a.UserID)
			}
			if !strings.Contains(haystack, needle) {
				continue
			}
		}
		out = append(out, &ActuaryPerformanceRow{
			UserID:        a.UserID,
			DisplayName:   name,
			Type:          a.ActuaryType,
			ProfitRSD:     a.ProfitRSD,
			RealizedCount: a.RealizedCount,
		})
	}
	return out, nil
}

// BankFundPositionRow wraps the existing decorated fund-position with
// the manager's display name.
type BankFundPositionRow struct {
	Position           *DecoratedFundPosition
	ManagerUserID      string
	ManagerDisplayName string
}

// ReassignSupervisorAssets flips every active fund managed by
// `fromUserID` over to `toUserID`. Internal-only entry point — called
// by user-svc from inside SetEmployeePermissions when the
// funds.manage.supervisor permission is being revoked, so a demoted
// supervisor never leaves orphaned funds behind (spec p.74 cascade).
//
// Permission: caller must hold `admin` (the user-svc adapter attaches
// the internal admin sentinel principal on the outgoing context).
// Idempotent: re-running with the same arguments after the flip
// returns count=0.
func (s *Service) ReassignSupervisorAssets(ctx context.Context, fromUserID, toUserID string) (int64, error) {
	if err := s.requirePermission(ctx, permissions.Admin); err != nil {
		return 0, err
	}
	if fromUserID == "" || toUserID == "" {
		return 0, apperr.Validation("from_user_id and to_user_id required")
	}
	if fromUserID == toUserID {
		return 0, apperr.Validation("from_user_id and to_user_id must differ")
	}
	if _, err := uuid.Parse(fromUserID); err != nil {
		return 0, apperr.Validation("from_user_id is not a UUID")
	}
	if _, err := uuid.Parse(toUserID); err != nil {
		return 0, apperr.Validation("to_user_id is not a UUID")
	}
	n, err := s.Store.ReassignFundManager(ctx, fromUserID, toUserID)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.Log.Info("reassigned funds on supervisor demotion",
			"from_user_id", fromUserID, "to_user_id", toUserID, "count", n)
	}
	return n, nil
}

// ListBankFundPositions returns every fund position held by the bank
// itself (`client_id = BankAsClientOwnerID`). The underlying
// FundPosition row already carries units + total_invested_rsd; the
// decoration step computes share_pct / current_value_rsd / profit_rsd.
// Gated by `bank.profit.read`.
func (s *Service) ListBankFundPositions(ctx context.Context) ([]*BankFundPositionRow, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.HasAny(p.Permissions, permissions.Admin, permissions.BankProfitRead) {
		return nil, apperr.PermissionDenied("nedovoljne permisije za Profit Banke")
	}
	rows, err := s.Store.ListFundPositions(ctx, store.FundPositionFilter{
		ClientID: BankAsClientOwnerID,
		Status:   "active",
	})
	if err != nil {
		return nil, err
	}
	out := make([]*BankFundPositionRow, 0, len(rows))
	for _, pos := range rows {
		f, err := s.Store.GetFund(ctx, pos.FundID)
		if err != nil {
			continue
		}
		dec := s.decorateFund(ctx, f)
		share, value, profit := positionDerivations(pos, dec)
		decPos := &DecoratedFundPosition{
			Position:        pos,
			Fund:            f,
			FundName:        f.Name,
			SharePct:        share,
			CurrentValueRSD: value,
			ProfitRSD:       profit,
		}
		mgrName := ""
		if s.Users != nil && f.ManagerUserID != "" {
			if n, err := s.Users.DisplayName(ctx, f.ManagerUserID, tdomain.KindEmployee); err == nil {
				mgrName = n
			}
		}
		out = append(out, &BankFundPositionRow{
			Position:           decPos,
			ManagerUserID:      f.ManagerUserID,
			ManagerDisplayName: mgrName,
		})
	}
	return out, nil
}
