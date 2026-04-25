package gateway

import (
	"context"
	"net/http"
	"strconv"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"
)

// holdingToJSON renders the portfolio row (spec p.62). asset_type tags the
// underlying so the UI can pick the right icon / route without reading the
// FK fields; profit is denominated in the booking account's currency.
func holdingToJSON(h *tradingpb.Holding) gin.H {
	return gin.H{
		"id":                 h.Id,
		"placer_id":          h.PlacerId,
		"asset_type":         h.AssetType,
		"stock_id":           h.StockId,
		"future_id":          h.FutureId,
		"forex_pair_id":      h.ForexPairId,
		"option_id":          h.OptionId,
		"ticker":             h.Ticker,
		"amount":             h.Amount,
		"avg_cost":           h.AvgCost,
		"current_price":      h.CurrentPrice,
		"profit":             h.Profit,
		"account_id":         h.AccountId,
		"account_number":     h.AccountNumber,
		"public_amount":      h.PublicAmount,
		"last_modified_unix": h.LastModifiedUnix,
	}
}

// ListPortfolio backs `GET /api/portfolio` (spec p.62). Open to both clients
// and employees; the trading server scopes results by the caller's identity
// — no caller_email needed since user-email metadata already rides on the
// gRPC call.
func (s *Server) ListPortfolio(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	resp, err := s.TradingClient.ListHoldings(ctx, &tradingpb.ListHoldingsRequest{
		CallerEmail: c.GetString("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	out := make([]gin.H, 0, len(resp.Holdings))
	for _, h := range resp.Holdings {
		out = append(out, holdingToJSON(h))
	}
	c.JSON(http.StatusOK, out)
}

type sellHoldingBody struct {
	HoldingID     int64  `json:"holding_id" binding:"required"`
	AccountNumber string `json:"account_number" binding:"required"`
	OrderType     string `json:"order_type" binding:"required"`
	Quantity      int64  `json:"quantity" binding:"required"`
	LimitPrice    int64  `json:"limit_price"`
	StopPrice     int64  `json:"stop_price"`
	AllOrNone     bool   `json:"all_or_none"`
}

// SellHolding backs `POST /api/portfolio/sell`. Forwards to the trading
// service's SellHolding wrapper, which constructs and dispatches the
// underlying CreateOrder. user-email metadata is attached so the trading
// server can authorize the holding owner.
func (s *Server) SellHolding(c *gin.Context) {
	var body sellHoldingBody
	if err := c.ShouldBindJSON(&body); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	resp, err := s.TradingClient.SellHolding(ctx, &tradingpb.SellHoldingRequest{
		HoldingId:     body.HoldingID,
		AccountNumber: body.AccountNumber,
		OrderType:     body.OrderType,
		Quantity:      body.Quantity,
		LimitPrice:    body.LimitPrice,
		StopPrice:     body.StopPrice,
		AllOrNone:     body.AllOrNone,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"order_id": resp.OrderId,
		"status":   resp.Status,
	})
}

type setHoldingPublicBody struct {
	PublicAmount int64 `json:"public_amount"`
}

// SetHoldingPublic backs `PATCH /api/portfolio/:id/public`. Owner-only; the
// trading server rejects non-stock holdings with FailedPrecondition (the
// schema CHECK pins public_amount=0 there) and out-of-range values with
// InvalidArgument.
func (s *Server) SetHoldingPublic(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "holding id must be a positive integer"})
		return
	}
	var body setHoldingPublicBody
	if err := c.ShouldBindJSON(&body); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	resp, err := s.TradingClient.SetHoldingPublic(ctx, &tradingpb.SetHoldingPublicRequest{
		HoldingId:    id,
		PublicAmount: body.PublicAmount,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, holdingToJSON(resp.Holding))
}

type exerciseOptionBody struct {
	AccountNumber string `json:"account_number" binding:"required"`
}

// ExerciseOption backs `POST /api/options/:id/exercise`. Actuary-only
// (employees only at the gateway, the trading server re-checks). The path
// id is the option contract id, account_number is the credit destination —
// must belong to the caller (verified by AuthorizeAccountAccess inside the
// trading RPC).
func (s *Server) ExerciseOption(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "option id must be a positive integer"})
		return
	}
	var body exerciseOptionBody
	if err := c.ShouldBindJSON(&body); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	resp, err := s.TradingClient.ExerciseOption(ctx, &tradingpb.ExerciseOptionRequest{
		OptionId:      id,
		AccountNumber: body.AccountNumber,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"payout":   resp.Payout,
		"quantity": resp.Quantity,
	})
}
