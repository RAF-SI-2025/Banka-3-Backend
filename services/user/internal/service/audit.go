package service

import (
	"context"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// recordAudit appends one audit entry attributed to the calling
// principal. Best-effort: a failed audit write is logged, never
// surfaced — it must not fail the business operation that triggered it.
func (s *Service) recordAudit(ctx context.Context, action, targetID, targetLabel, oldVal, newVal, note string) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		s.Log.WarnContext(ctx, "audit skipped: no principal", "action", action, "target_id", targetID)
		return
	}
	if err := s.Store.InsertAudit(ctx, &domain.AuditEntry{
		Action:      action,
		ActorID:     p.UserID,
		ActorKind:   string(p.UserKind),
		ActorName:   s.resolveActorName(ctx, p.UserID, string(p.UserKind)),
		TargetID:    targetID,
		TargetLabel: targetLabel,
		OldValue:    oldVal,
		NewValue:    newVal,
		Note:        note,
	}); err != nil {
		// Swallowed by design: a failed audit write must not fail the
		// business operation that triggered it.
		s.Log.WarnContext(ctx, "audit insert failed", "err", err, "action", action, "target_id", targetID)
	}
}

// resolveActorName looks up an employee's display name for denormalized
// storage (so the audit list + name filter don't need a cross-service
// join). Best-effort: returns "" when the actor isn't a resolvable
// employee.
func (s *Service) resolveActorName(ctx context.Context, userID, kind string) string {
	if kind != string(domain.KindEmployee) || userID == "" {
		return ""
	}
	e, err := s.Store.GetEmployeeByID(ctx, userID)
	if err != nil || e == nil {
		if err != nil && !isNotFound(err) {
			// Swallowed by design: the audit row still lands without a
			// display name. The store already logged the query error.
			s.Log.WarnContext(ctx, "resolve actor name failed", "err", err, "user_id", userID)
		}
		return ""
	}
	return strings.TrimSpace(e.FirstName + " " + e.LastName)
}

// ListAuditLog returns audit entries newest-first. Admin + supervisor
// only; clients (and other employees) are denied (S46).
func (s *Service) ListAuditLog(ctx context.Context, f domain.AuditFilter, page, pageSize int) ([]*domain.AuditEntry, int64, error) {
	if err := s.requireAuditReader(ctx); err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return s.Store.ListAudit(ctx, f, pageSize, (page-1)*pageSize)
}

// RecordAuditEntry is the cross-service write path (trading/bank call it
// via gRPC). When the entry omits an actor, the calling principal is
// used; the display name is resolved when absent.
func (s *Service) RecordAuditEntry(ctx context.Context, e domain.AuditEntry) error {
	if e.ActorID == "" {
		if p, ok := auth.PrincipalFrom(ctx); ok {
			e.ActorID = p.UserID
			e.ActorKind = string(p.UserKind)
		}
	}
	if e.ActorName == "" {
		e.ActorName = s.resolveActorName(ctx, e.ActorID, e.ActorKind)
	}
	return s.Store.InsertAudit(ctx, &e)
}

// requireAuditReader gates audit-log reads to admins + supervisors.
func (s *Service) requireAuditReader(ctx context.Context) error {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return apperr.Unauthenticated("not authenticated")
	}
	if permissions.Has(p.Permissions, permissions.Admin) ||
		permissions.Has(p.Permissions, permissions.ActuarySupervisor) {
		return nil
	}
	s.Log.WarnContext(ctx, "audit read denied", "user_id", p.UserID, "kind", p.UserKind)
	return apperr.PermissionDenied("nedovoljne permisije")
}
