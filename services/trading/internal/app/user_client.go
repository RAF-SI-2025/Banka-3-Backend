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

func (a *userResolverAdapter) DisplayName(ctx context.Context, userID string, kind domain.UserKind) (string, error) {
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "trading-service-internal",
		UserKind:    auth.KindEmployee,
		Permissions: []string{"admin", "client.read", "employee.read"},
	})
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
