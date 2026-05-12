// Package service — SAGA registry wiring for c4.
//
// RegisterSagas hooks every c4 saga Definition into the orchestrator's
// registry. Drivers live next to their service-layer caller (e.g.
// otc_accept_saga.go beside the OTC service in PR2). PR1 lands the
// wiring; PR2/PR3 register the actual OTC + fund definitions.

package service

import (
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
)

// RegisterSagas registers every saga Definition the service knows
// about with `reg`. Called once at app boot before the orchestrator
// starts driving rows.
//
// PR1 leaves this empty — the registry is wired, the recovery worker
// is running, and the bank reservation primitives are in place; PR2's
// OTC backend lands the first real Definition (otc_accept_saga).
// Migrating ExerciseOption to a saga (FOUND-2) ships immediately
// before PR2 so its first integration use validates the framework.
func RegisterSagas(reg *saga.Registry, svc *Service) {
	_ = reg
	_ = svc
}
