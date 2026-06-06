package app

import (
	"context"
	"fmt"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// userResolverAdapter implements service.UserResolver on top of the
// user-service gRPC client. The trading service calls this from the
// supervisor tax dashboard (no end-user principal forwarded — the
// resolver runs server-side after requireSupervisor has admitted the
// caller), so we attach an internal admin principal to outgoing
// metadata. Same trust model as bank's user_client.go.
type userResolverAdapter struct {
	c userpb.UserServiceClient
}

// withUserAdmin attaches the trading service's internal admin principal
// to outgoing metadata. These calls run server-side after the relevant
// permission gate has already admitted the caller, so there is no
// end-user principal to forward.
func withUserAdmin(ctx context.Context) context.Context {
	return auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "trading-service-internal",
		UserKind:    auth.KindEmployee,
		Permissions: []string{"admin", "client.read", "employee.read"},
	})
}

func (a *userResolverAdapter) DisplayName(ctx context.Context, userID string, kind domain.UserKind) (string, error) {
	ctx = withUserAdmin(ctx)
	switch kind {
	case domain.KindClient:
		resp, err := a.c.GetClient(ctx, &userpb.GetClientRequest{Id: userID})
		if err != nil {
			return "", fmt.Errorf("user.GetClient: %w", err)
		}
		return joinName(resp.GetFirstName(), resp.GetLastName()), nil
	case domain.KindEmployee:
		resp, err := a.c.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: userID})
		if err != nil {
			return "", fmt.Errorf("user.GetEmployee: %w", err)
		}
		return joinName(resp.GetFirstName(), resp.GetLastName()), nil
	default:
		return "", nil
	}
}

// RecordAudit forwards one audit entry to user-svc. The transport uses
// the same internal admin principal as the other adapter methods (the
// RecordAuditEntry RPC has no permission gate), but the actor_id /
// actor_kind carried in the request body are the real caller resolved
// trading-side, so the persisted audit row attributes the action to the
// supervisor/admin who actually performed it rather than the sentinel.
func (a *userResolverAdapter) RecordAudit(ctx context.Context, action, actorID, actorKind, targetID, targetLabel, oldVal, newVal, note string) error {
	ctx = withUserAdmin(ctx)
	_, err := a.c.RecordAuditEntry(ctx, &userpb.RecordAuditEntryRequest{
		Action:      action,
		ActorId:     actorID,
		ActorKind:   actorKind,
		TargetId:    targetID,
		TargetLabel: targetLabel,
		OldValue:    oldVal,
		NewValue:    newVal,
		Note:        note,
	})
	if err != nil {
		return fmt.Errorf("user.RecordAuditEntry: %w", err)
	}
	return nil
}

func (a *userResolverAdapter) EmployeePermissions(ctx context.Context, userID string) ([]string, error) {
	ctx = withUserAdmin(ctx)
	resp, err := a.c.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: userID})
	if err != nil {
		return nil, fmt.Errorf("user.GetEmployee: %w", err)
	}
	return resp.GetPermissions(), nil
}

func joinName(first, last string) string {
	switch {
	case first == "" && last == "":
		return ""
	case first == "":
		return last
	case last == "":
		return first
	default:
		return first + " " + last
	}
}
