package server

import (
	"context"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
)

// RunMaintenanceFeeJob fires the monthly maintenance-fee debit pass.
// Normally driven by the scheduler service; admin-only via the gateway.
func (s *Server) RunMaintenanceFeeJob(ctx context.Context, in *bankpb.RunMaintenanceFeeJobRequest) (*bankpb.RunMaintenanceFeeJobResponse, error) {
	asOf := time.Time{}
	if v := in.GetAsOf(); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return nil, apperr.Validation("as_of must be YYYY-MM-DD")
		}
		asOf = t
	}
	r, err := s.Svc.RunMaintenanceFeeJob(ctx, asOf)
	if err != nil {
		return nil, err
	}
	return &bankpb.RunMaintenanceFeeJobResponse{
		Processed: int32(r.Processed),
		Charged:   int32(r.Charged),
		Skipped:   int32(r.Skipped),
	}, nil
}

// RunSpentResetJob fires the daily/monthly spent-counter rollover.
// Normally driven by the scheduler service; admin-only via the gateway.
func (s *Server) RunSpentResetJob(ctx context.Context, _ *bankpb.RunSpentResetJobRequest) (*bankpb.RunSpentResetJobResponse, error) {
	r, err := s.Svc.RunSpentResetJob(ctx)
	if err != nil {
		return nil, err
	}
	return &bankpb.RunSpentResetJobResponse{Daily: r.Daily, Monthly: r.Monthly}, nil
}
