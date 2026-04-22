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

// tradingCtx builds a 5s gRPC context carrying the caller's email. The
// trading server's ResolveCaller reads user-email from metadata the same
// way bank does — see internal/bank/account.go.
func tradingCtx(c *gin.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))
	return ctx, cancel
}

func (s *Server) ListExchanges(c *gin.Context) {
	ctx, cancel := tradingCtx(c)
	defer cancel()

	resp, err := s.TradingClient.ListExchanges(ctx, &tradingpb.ListExchangesRequest{})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	out := make([]gin.H, 0, len(resp.Exchanges))
	for _, e := range resp.Exchanges {
		out = append(out, exchangeToJSON(e))
	}
	c.JSON(http.StatusOK, out)
}

func exchangeToJSON(e *tradingpb.Exchange) gin.H {
	return gin.H{
		"id":               e.Id,
		"name":             e.Name,
		"acronym":          e.Acronym,
		"mic_code":         e.MicCode,
		"polity":           e.Polity,
		"currency":         e.Currency,
		"time_zone_offset": e.TimeZoneOffset,
		"open_time":        e.OpenTime,
		"close_time":       e.CloseTime,
		"open_override":    e.OpenOverride,
	}
}

// parseInt64Query reads an int64 query param; empty string returns 0. An
// unparseable value surfaces a 400.
func parseInt64Query(c *gin.Context, key string) (int64, bool) {
	raw := c.Query(key)
	if raw == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": key + " must be an integer"})
		return 0, false
	}
	return v, true
}

func (s *Server) ListListings(c *gin.Context) {
	priceMin, ok := parseInt64Query(c, "price_min")
	if !ok {
		return
	}
	priceMax, ok := parseInt64Query(c, "price_max")
	if !ok {
		return
	}
	askMin, ok := parseInt64Query(c, "ask_min")
	if !ok {
		return
	}
	askMax, ok := parseInt64Query(c, "ask_max")
	if !ok {
		return
	}
	bidMin, ok := parseInt64Query(c, "bid_min")
	if !ok {
		return
	}
	bidMax, ok := parseInt64Query(c, "bid_max")
	if !ok {
		return
	}
	volMin, ok := parseInt64Query(c, "volume_min")
	if !ok {
		return
	}
	volMax, ok := parseInt64Query(c, "volume_max")
	if !ok {
		return
	}
	settleFrom, ok := parseInt64Query(c, "settlement_from")
	if !ok {
		return
	}
	settleTo, ok := parseInt64Query(c, "settlement_to")
	if !ok {
		return
	}

	ctx, cancel := tradingCtx(c)
	defer cancel()

	resp, err := s.TradingClient.ListListings(ctx, &tradingpb.ListListingsRequest{
		CallerEmail:        c.GetString("email"),
		ExchangePrefix:     c.Query("exchange"),
		Search:             c.Query("search"),
		PriceMin:           priceMin,
		PriceMax:           priceMax,
		AskMin:             askMin,
		AskMax:             askMax,
		BidMin:             bidMin,
		BidMax:             bidMax,
		VolumeMin:          volMin,
		VolumeMax:          volMax,
		SettlementFromUnix: settleFrom,
		SettlementToUnix:   settleTo,
		SortBy:             c.Query("sort_by"),
		SortOrder:          c.Query("sort_order"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	out := make([]gin.H, 0, len(resp.Listings))
	for _, l := range resp.Listings {
		out = append(out, listingToJSON(l))
	}
	c.JSON(http.StatusOK, out)
}

func listingToJSON(l *tradingpb.Listing) gin.H {
	return gin.H{
		"id":                  l.Id,
		"exchange_id":         l.ExchangeId,
		"exchange_acronym":    l.ExchangeAcronym,
		"security_type":       l.SecurityType,
		"stock_id":            l.StockId,
		"future_id":           l.FutureId,
		"ticker":              l.Ticker,
		"name":                l.Name,
		"price":               l.Price,
		"ask_price":           l.AskPrice,
		"bid_price":           l.BidPrice,
		"volume":              l.Volume,
		"change":              l.Change,
		"last_refresh":        l.LastRefreshUnix,
		"settlement_date":     l.SettlementDateUnix,
		"contract_size":       l.ContractSize,
		"maintenance_margin":  l.MaintenanceMargin,
		"initial_margin_cost": l.InitialMarginCost,
		"market_cap":          l.MarketCap,
		"nominal_value":       l.NominalValue,
	}
}

func (s *Server) GetListing(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id must be a positive integer"})
		return
	}
	ctx, cancel := tradingCtx(c)
	defer cancel()

	resp, err := s.TradingClient.GetListing(ctx, &tradingpb.GetListingRequest{
		Id:          id,
		CallerEmail: c.GetString("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, listingToJSON(resp.Listing))
}

func (s *Server) GetListingHistory(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id must be a positive integer"})
		return
	}
	period := c.DefaultQuery("period", "month")

	ctx, cancel := tradingCtx(c)
	defer cancel()

	resp, err := s.TradingClient.ListListingHistory(ctx, &tradingpb.ListListingHistoryRequest{
		ListingId:   id,
		Period:      period,
		CallerEmail: c.GetString("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	points := make([]gin.H, 0, len(resp.Points))
	for _, p := range resp.Points {
		points = append(points, gin.H{
			"date":      p.DateUnix,
			"price":     p.Price,
			"ask_price": p.AskPrice,
			"bid_price": p.BidPrice,
			"change":    p.Change,
			"volume":    p.Volume,
		})
	}
	c.JSON(http.StatusOK, gin.H{"period": period, "points": points})
}

func (s *Server) ListForexPairs(c *gin.Context) {
	ctx, cancel := tradingCtx(c)
	defer cancel()

	resp, err := s.TradingClient.ListForexPairs(ctx, &tradingpb.ListForexPairsRequest{
		CallerEmail: c.GetString("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	out := make([]gin.H, 0, len(resp.Pairs))
	for _, p := range resp.Pairs {
		out = append(out, gin.H{
			"id":                 p.Id,
			"ticker":             p.Ticker,
			"name":               p.Name,
			"base_currency":      p.BaseCurrency,
			"quote_currency":     p.QuoteCurrency,
			"exchange_rate":      p.ExchangeRate,
			"liquidity":          p.Liquidity,
			"contract_size":      p.ContractSize,
			"maintenance_margin": p.MaintenanceMargin,
			"nominal_value":      p.NominalValue,
		})
	}
	c.JSON(http.StatusOK, out)
}
