package gateway

import (
	"context"
	"net/http"
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
