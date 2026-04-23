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

type createOrderBody struct {
	ListingID     int64  `json:"listing_id"`
	OptionID      int64  `json:"option_id"`
	ForexPairID   int64  `json:"forex_pair_id"`
	AccountNumber string `json:"account_number" binding:"required"`
	OrderType     string `json:"order_type" binding:"required"`
	Direction     string `json:"direction" binding:"required"`
	Quantity      int64  `json:"quantity" binding:"required"`
	LimitPrice    int64  `json:"limit_price"`
	StopPrice     int64  `json:"stop_price"`
	AllOrNone     bool   `json:"all_or_none"`
	Margin        bool   `json:"margin"`
}

func (s *Server) CreateOrder(c *gin.Context) {
	var body createOrderBody
	if err := c.ShouldBindJSON(&body); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	resp, err := s.TradingClient.CreateOrder(ctx, &tradingpb.CreateOrderRequest{
		ListingId:     body.ListingID,
		OptionId:      body.OptionID,
		ForexPairId:   body.ForexPairID,
		AccountNumber: body.AccountNumber,
		OrderType:     body.OrderType,
		Direction:     body.Direction,
		Quantity:      body.Quantity,
		LimitPrice:    body.LimitPrice,
		StopPrice:     body.StopPrice,
		AllOrNone:     body.AllOrNone,
		Margin:        body.Margin,
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

// orderDetailToJSON renders the supervisor portal's order row (spec p.57).
// Shape mirrors the proto's OrderDetail so frontend fields stay in lockstep.
func orderDetailToJSON(o *tradingpb.OrderDetail) gin.H {
	return gin.H{
		"id":                 o.Id,
		"status":             o.Status,
		"order_type":         o.OrderType,
		"direction":          o.Direction,
		"quantity":           o.Quantity,
		"contract_size":      o.ContractSize,
		"price_per_unit":     o.PricePerUnit,
		"remaining_portions": o.RemainingPortions,
		"asset_label":        o.AssetLabel,
		"agent":              o.PlacerName,
		"placer_employee_id": o.PlacerEmployeeId,
		"placer_client_id":   o.PlacerClientId,
		"past_settlement":    o.PastSettlement,
		"approved_by":        o.ApprovedBy,
		"created_at_unix":    o.CreatedAtUnix,
		"margin":             o.Margin,
		"all_or_none":        o.AllOrNone,
		"commission":         o.Commission,
	}
}

// ListOrders surfaces the supervisor's orders portal feed. Both filters are
// optional: `status` accepts all|pending|approved|declined|done|cancelled,
// `agent` accepts an employee id to scope down to a single actuary.
func (s *Server) ListOrders(c *gin.Context) {
	statusFilter := c.Query("status")
	agentStr := c.Query("agent")
	var agentID int64
	if agentStr != "" {
		v, err := strconv.ParseInt(agentStr, 10, 64)
		if err != nil || v < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "agent must be a non-negative integer"})
			return
		}
		agentID = v
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.TradingClient.ListOrders(ctx, &tradingpb.ListOrdersRequest{
		CallerEmail: c.GetString("email"),
		Status:      statusFilter,
		AgentId:     agentID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	out := make([]gin.H, 0, len(resp.Orders))
	for _, o := range resp.Orders {
		out = append(out, orderDetailToJSON(o))
	}
	c.JSON(http.StatusOK, out)
}

// orderIDFromPath parses the `:id` path segment into a positive int64. Shared
// between approve/decline/cancel so the 400 message is consistent.
func orderIDFromPath(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "order id must be a positive integer"})
		return 0, false
	}
	return id, true
}

func (s *Server) ApproveOrder(c *gin.Context) {
	id, ok := orderIDFromPath(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	resp, err := s.TradingClient.ApproveOrder(ctx, &tradingpb.ApproveOrderRequest{
		OrderId:     id,
		CallerEmail: c.GetString("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, orderDetailToJSON(resp.Order))
}

func (s *Server) DeclineOrder(c *gin.Context) {
	id, ok := orderIDFromPath(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	resp, err := s.TradingClient.DeclineOrder(ctx, &tradingpb.DeclineOrderRequest{
		OrderId:     id,
		CallerEmail: c.GetString("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, orderDetailToJSON(resp.Order))
}

// CancelOrder sends the caller's email via gRPC metadata (mirroring
// CreateOrder) so the trading server can distinguish client-owner vs.
// employee-owner vs. supervisor without an extra caller_email field.
func (s *Server) CancelOrder(c *gin.Context) {
	id, ok := orderIDFromPath(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))
	resp, err := s.TradingClient.CancelOrder(ctx, &tradingpb.CancelOrderRequest{
		OrderId: id,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, orderDetailToJSON(resp.Order))
}
