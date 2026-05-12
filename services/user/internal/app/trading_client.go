package app

import (
	"context"
	"fmt"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
)

// fundReassignerAdapter implements service.FundReassigner on top of the
// trading-service gRPC client. User-svc calls this from
// SetEmployeePermissions when the funds.manage.supervisor permission
// is being revoked (c4 PR4 CASCADE-1, spec p.74). The caller is the
// acting admin, but user-svc runs the cascade server-side after
// permission gating has already admitted them; we attach an internal
// admin sentinel principal on the outgoing metadata so the trading
// service's admin-only ReassignSupervisorAssets RPC accepts the call.
type fundReassignerAdapter struct {
	c tradingpb.TradingServiceClient
}

func (a *fundReassignerAdapter) Reassign(ctx context.Context, fromUserID, toUserID string) (int64, error) {
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "user-service-internal",
		UserKind:    auth.KindEmployee,
		Permissions: []string{"admin"},
	})
	resp, err := a.c.ReassignSupervisorAssets(ctx, &tradingpb.ReassignSupervisorAssetsRequest{
		FromUserId: fromUserID,
		ToUserId:   toUserID,
	})
	if err != nil {
		return 0, fmt.Errorf("trading.ReassignSupervisorAssets: %w", err)
	}
	return int64(resp.GetFundsReassigned()), nil
}
