package gateway

import (
	"context"
	"net/http"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"
)

func (s *Server) GetCards(c *gin.Context) {
	email := c.GetString("email")

	md := metadata.Pairs("user-email", email)
	ctx := metadata.NewOutgoingContext(c.Request.Context(), md)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.GetCards(ctx, &bankpb.GetCardsRequest{})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	cards := make([]gin.H, 0, len(resp.Cards))
	for _, card := range resp.Cards {
		cards = append(cards, gin.H{
			"card_number":     card.CardNumber,
			"card_type":       card.CardType,
			"card_name":       card.CardBrand,
			"creation_date":   card.CreationDate,
			"expiration_date": card.ExpirationDate,
			"account_number":  card.AccountNumber,
			"cvv":             card.Cvv,
			"limit":           card.Limit,
			"status":          card.Status,
		})
	}

	c.JSON(http.StatusOK, cards)
}

func (s *Server) RequestCard(c *gin.Context) {
	email := c.GetString("email")

	var req requestCardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.CreateCard(ctx, &bankpb.CreateCardRequest{
		Email:         email,
		AccountNumber: req.AccountNumber,
		CardType:      req.CardType,
		CardBrand:     req.CardBrand,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	// Send notification email (best-effort)
	go func() {
		notifCtx, notifCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer notifCancel()
		_, _ = s.NotificationClient.SendCardCreatedEmail(notifCtx, &notificationpb.CardCreatedMailRequest{
			ToAddr: email,
		})
	}()

	c.JSON(http.StatusCreated, gin.H{
		"card_number":    resp.CardNumber,
		"account_number": req.AccountNumber,
		"card_type":      req.CardType,
		"card_brand":     req.CardBrand,
	})
}

func (s *Server) ConfirmCard(c *gin.Context) {
	var query confirmCardQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	_, err := s.BankClient.ConfirmCard(ctx, &bankpb.ConfirmCardRequest{
		Token: query.Token,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Status(http.StatusOK)
}

func (s *Server) BlockCard(c *gin.Context) {
	email := c.GetString("email")

	var uri blockCardURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "card number is required")
		return
	}

	md := metadata.Pairs("user-email", email)
	ctx := metadata.NewOutgoingContext(c.Request.Context(), md)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := s.BankClient.BlockCard(ctx, &bankpb.BlockCardRequest{
		CardNumber: uri.CardNumber,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Status(http.StatusOK)
}
