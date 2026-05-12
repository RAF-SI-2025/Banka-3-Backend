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
// c4-PR2 added otc_accept + otc_exercise; c4-PR3 adds fund_invest +
// fund_withdraw. PR4 will add profit-cascade.
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
