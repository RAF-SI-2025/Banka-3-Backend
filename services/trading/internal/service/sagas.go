// Package service — SAGA registry wiring.
//
// RegisterSagas hooks every saga Definition into the orchestrator's
// registry. Drivers live next to their service-layer caller (e.g.
// otc_accept_saga.go beside the OTC service).

package service

import (
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
)

// RegisterSagas registers every saga Definition the service knows
// about with `reg`. Called once at app boot before the orchestrator
// starts driving rows.
//
//   - otc_accept    — accept saga (premium leg + contract mint).
//   - otc_exercise  — buyer exercises a contract (strike leg + shares
//     transfer + seller realized_gain).
//   - fund_invest   — client/supervisor invests into a fund (reserve →
//     transfer → position upsert + units mint).
//   - fund_withdraw — liquid + illiquid withdraw (reserve / liquidate
//     → transfer → position decrement + realized_gain).
func RegisterSagas(reg *saga.Registry, svc *Service) {
	registerOTCAcceptSaga(reg, svc)
	registerOTCExerciseSaga(reg, svc)
	registerFundInvestSaga(reg, svc)
	registerFundWithdrawSaga(reg, svc)
}
