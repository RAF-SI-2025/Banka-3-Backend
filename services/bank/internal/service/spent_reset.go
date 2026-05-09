package service

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
)

// SpentResetJobResult summarises one cron run.
type SpentResetJobResult struct {
	Daily   int64 // accounts whose daily_spent was reset
	Monthly int64 // accounts whose monthly_spent was reset
}

// RunSpentResetJob zeroes the daily_spent and monthly_spent counters
// per spec p.12-13. The daily counter resets every calendar day; the
// monthly counter resets on the first day of each month. Both passes
// are idempotent (a second run on the same day touches zero rows) so
// the cron can fire as often as we like; the default is hourly so a
// cold-start within an hour of midnight still rolls everyone over
// before the next debit.
//
// Admin-only when invoked manually; the cron path uses RunSpentResetJobAuto.
func (s *Service) RunSpentResetJob(ctx context.Context) (*SpentResetJobResult, error) {
	if err := s.requirePermission(ctx, permissions.Admin); err != nil {
		return nil, err
	}
	return s.runSpentResetJob(ctx)
}

// RunSpentResetJobAuto is the un-authenticated entry used by the
// in-process cron.
func (s *Service) RunSpentResetJobAuto(ctx context.Context) error {
	res, err := s.runSpentResetJob(ctx)
	if err != nil {
		return err
	}
	if res.Daily > 0 || res.Monthly > 0 {
		s.Log.Info("spent reset job ran", "daily", res.Daily, "monthly", res.Monthly)
	}
	return nil
}

func (s *Service) runSpentResetJob(ctx context.Context) (*SpentResetJobResult, error) {
	daily, monthly, err := s.Store.ResetSpentCounters(ctx)
	if err != nil {
		return nil, err
	}
	return &SpentResetJobResult{Daily: daily, Monthly: monthly}, nil
}
