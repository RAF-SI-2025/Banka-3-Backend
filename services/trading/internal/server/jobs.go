package server

import (
	"context"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
)

// requireAdmin gates the job-trigger RPCs at the handler layer. The
// in-process cron bypasses these handlers (it calls the service methods
// directly with a cron-internal context), and the scheduler service
// presents the admin-sentinel principal — so this only rejects an
// unprivileged caller arriving via the gateway.
func requireAdmin(ctx context.Context) error {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return apperr.Unauthenticated("not authenticated")
	}
	if permissions.Has(p.Permissions, permissions.Admin) {
		return nil
	}
	return apperr.PermissionDenied("nedovoljne permisije")
}

// RunExecutionTick runs one partial-fill pass. Internal-only RPC driven
// by the scheduler service.
func (s *Server) RunExecutionTick(ctx context.Context, _ *tradingpb.RunExecutionTickRequest) (*tradingpb.RunExecutionTickResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	fired, err := s.Svc.RunExecutionTick(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunExecutionTickResponse{Fired: int32(fired)}, nil
}

// RunSagaRecoveryTick resumes sagas due for recovery. Internal-only RPC.
func (s *Server) RunSagaRecoveryTick(ctx context.Context, _ *tradingpb.RunSagaRecoveryTickRequest) (*tradingpb.RunSagaRecoveryTickResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	n, err := s.Svc.RunSagaRecoveryTick(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunSagaRecoveryTickResponse{Resumed: int32(n)}, nil
}

// RunOTCExpirySweep expires OTC contracts past settlement. Admin-only.
func (s *Server) RunOTCExpirySweep(ctx context.Context, _ *tradingpb.RunOTCExpirySweepRequest) (*tradingpb.RunOTCExpirySweepResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	r, err := s.Svc.SweepExpiredOTCContracts(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunOTCExpirySweepResponse{
		ContractsExpired: int32(r.ContractsExpired),
		SharesReleased:   r.SharesReleased,
		OffersExpired:    int32(r.OffersExpired),
		OffersWarned:     int32(r.OffersWarned),
	}, nil
}

// RunOptionsRefresh regenerates option chains. Admin-only. No-op when no
// option generator is wired.
func (s *Server) RunOptionsRefresh(ctx context.Context, _ *tradingpb.RunOptionsRefreshRequest) (*tradingpb.RunOptionsRefreshResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s.Svc.Options == nil {
		return &tradingpb.RunOptionsRefreshResponse{}, nil
	}
	r, err := s.Svc.Options.RunOnce(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunOptionsRefreshResponse{
		UnderlyingsProcessed: int32(r.UnderlyingsProcessed),
		OptionsUpserted:      int32(r.OptionsUpserted),
		Skipped:              int32(r.Skipped),
	}, nil
}

// RunMarketDataRefresh refreshes upstream stock + forex quotes.
// Admin-only. No-op when no upstream is configured.
func (s *Server) RunMarketDataRefresh(ctx context.Context, _ *tradingpb.RunMarketDataRefreshRequest) (*tradingpb.RunMarketDataRefreshResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s.Svc.MarketData == nil {
		return &tradingpb.RunMarketDataRefreshResponse{}, nil
	}
	r, err := s.Svc.MarketData.RunOnce(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunMarketDataRefreshResponse{
		StocksUpdated:     int32(r.StocksUpdated),
		ForexUpdated:      int32(r.ForexUpdated),
		Skipped:           int32(r.Skipped),
		UpstreamErrors:    int32(r.UpstreamErrors),
		UpstreamThrottled: r.UpstreamThrottled,
	}, nil
}

// RunStockHistoryBackfill backfills daily-close history. Admin-only.
// No-op when no history upstream is configured.
func (s *Server) RunStockHistoryBackfill(ctx context.Context, _ *tradingpb.RunStockHistoryBackfillRequest) (*tradingpb.RunStockHistoryBackfillResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s.Svc.MarketData == nil || s.Svc.MarketData.History == nil {
		return &tradingpb.RunStockHistoryBackfillResponse{}, nil
	}
	r, err := s.Svc.MarketData.BackfillStockHistory(ctx)
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunStockHistoryBackfillResponse{
		SymbolsBackfilled: int32(r.SymbolsBackfilled),
		RowsWritten:       int32(r.RowsWritten),
		Skipped:           int32(r.Skipped),
		UpstreamErrors:    int32(r.UpstreamErrors),
		UpstreamThrottled: r.UpstreamThrottled,
	}, nil
}

// RunFundPerformanceSnapshot writes one snapshot per active fund.
// Admin-only.
func (s *Server) RunFundPerformanceSnapshot(ctx context.Context, _ *tradingpb.RunFundPerformanceSnapshotRequest) (*tradingpb.RunFundPerformanceSnapshotResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	n, err := s.Svc.SnapshotAllFunds(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	return &tradingpb.RunFundPerformanceSnapshotResponse{Funds: int32(n)}, nil
}
