package gateway

import (
	"context"
	"net/http"
	"strconv"
	"time"

	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/exchange"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/status"
)

func (s *Server) GetExchangeRates(c *gin.Context) {
	resp, err := s.ExchangeClient.GetExchangeRates(c.Request.Context(), &exchangepb.ExchangeRateListRequest{})
	if err != nil {
		st, _ := status.FromError(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": st.Message()})
		return
	}

	rates := make([]gin.H, 0, len(resp.Rates))
	for _, r := range resp.Rates {
		// If proto doesn't have buy/sell/middle yet, derive at gateway
		middleRate := r.MiddleRate
		buyRate := r.BuyRate
		sellRate := r.SellRate
		if middleRate == 0 {
			middleRate = r.Rate
		}
		if buyRate == 0 {
			buyRate = r.Rate * 0.995
		}
		if sellRate == 0 {
			sellRate = r.Rate * 1.005
		}

		rates = append(rates, gin.H{
			"currencyCode": r.Code,
			"buyRate":      buyRate,
			"sellRate":     sellRate,
			"middleRate":   middleRate,
		})
	}

	c.JSON(http.StatusOK, rates)
}

type setExchangeOpenOverrideBody struct {
	OpenOverride *bool `json:"open_override" binding:"required"`
}

// SetExchangeOpenOverride flips the open_override flag on an exchange.
// Supervisor-only — route is gated at `secured("supervisor")` and the trading
// RPC re-checks the caller's permissions against employee_permissions.
func (s *Server) SetExchangeOpenOverride(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "exchange id must be a positive integer"})
		return
	}

	var body setExchangeOpenOverrideBody
	if err := c.ShouldBindJSON(&body); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.TradingClient.SetExchangeOpenOverride(ctx, &tradingpb.SetExchangeOpenOverrideRequest{
		ExchangeId:   id,
		OpenOverride: *body.OpenOverride,
		CallerEmail:  c.GetString("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	ex := resp.Exchange
	c.JSON(http.StatusOK, gin.H{
		"id":               ex.Id,
		"name":             ex.Name,
		"acronym":          ex.Acronym,
		"mic_code":         ex.MicCode,
		"polity":           ex.Polity,
		"currency":         ex.Currency,
		"time_zone_offset": ex.TimeZoneOffset,
		"open_time":        ex.OpenTime,
		"close_time":       ex.CloseTime,
		"open_override":    ex.OpenOverride,
	})
}

func (s *Server) ConvertMoney(c *gin.Context) {
	var req conversionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := s.ExchangeClient.ConvertMoney(c.Request.Context(), &exchangepb.ConversionRequest{
		FromCurrency: req.FromCurrency,
		ToCurrency:   req.ToCurrency,
		Amount:       req.Amount,
	})
	if err != nil {
		st, _ := status.FromError(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": st.Message()})
		return
	}

	c.JSON(http.StatusOK, resp)
}
