package gateway

import (
	"context"
	"net/http"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"github.com/gin-gonic/gin"
)

// RunCapitalGains backs `POST /api/tax/run?month=YYYY-MM`. Supervisor-only;
// the trading RPC re-checks the caller's permissions. Empty `month` runs a
// back-fill across every still-unpaid period (spec p.63 doesn't define a
// strict semantics for the manual button — letting the supervisor scope the
// run by month is the more useful version).
func (s *Server) RunCapitalGains(c *gin.Context) {
	month := c.Query("month")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	resp, err := s.TradingClient.RunCapitalGains(ctx, &tradingpb.RunCapitalGainsRequest{
		CallerEmail: c.GetString("email"),
		Month:       month,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"period":         resp.Period,
		"accounts_paid":  resp.AccountsPaid,
		"rows_paid":      resp.RowsPaid,
		"insufficient":   resp.Insufficient,
		"total_debt_rsd": resp.TotalDebtRsd,
		"collected_rsd":  resp.CollectedRsd,
	})
}

// ListTaxDebts backs `GET /api/tax/debts?team=client|actuary&name=...`.
// Supervisor-only; the trading RPC re-checks the caller's permissions and
// validates the team filter (anything other than client/actuary/empty
// returns InvalidArgument).
func (s *Server) ListTaxDebts(c *gin.Context) {
	team := c.Query("team")
	name := c.Query("name")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.TradingClient.ListTaxDebts(ctx, &tradingpb.ListTaxDebtsRequest{
		CallerEmail: c.GetString("email"),
		Team:        team,
		Name:        name,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	out := make([]gin.H, 0, len(resp.Debtors))
	for _, d := range resp.Debtors {
		out = append(out, gin.H{
			"user_id":    d.UserId,
			"first_name": d.FirstName,
			"last_name":  d.LastName,
			"team":       d.Team,
			"unpaid_rsd": d.UnpaidRsd,
			"paid_rsd":   d.PaidRsd,
		})
	}
	c.JSON(http.StatusOK, out)
}
