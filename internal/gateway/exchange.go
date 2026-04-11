package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/exchange"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
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

func (s *Server) ConvertMoney(c *gin.Context) {
	var req conversionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Keep calculator behavior for existing callers, but execute a real
	// account-to-account conversion when the client supplies account numbers.
	if strings.TrimSpace(req.FromAccount) != "" || strings.TrimSpace(req.ToAccount) != "" {
		if strings.TrimSpace(req.FromAccount) == "" || strings.TrimSpace(req.ToAccount) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "from_account and to_account are both required for persisted conversion"})
			return
		}

		totpCode := strings.TrimSpace(c.GetHeader("TOTP"))
		if totpCode == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "missing TOTP code"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		verifyResp, err := s.TOTPClient.VerifyCode(ctx, &userpb.VerifyCodeRequest{
			Email: c.GetString("email"),
			Code:  totpCode,
		})
		if err != nil {
			writeGRPCError(c, err)
			return
		}
		if !verifyResp.Valid {
			if verifyResp.TransactionCancelled {
				c.JSON(http.StatusUnauthorized, gin.H{
					"message":            "verification code expired or transaction cancelled",
					"remaining_attempts": verifyResp.RemainingAttempts,
				})
				return
			}
			c.JSON(http.StatusUnauthorized, gin.H{
				"message":            "invalid verification code",
				"remaining_attempts": verifyResp.RemainingAttempts,
			})
			return
		}

		amount, err := normalizeMoneyAmount(req.Amount)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		transferResp, err := s.BankClient.TransferMoneyBetweenAccounts(ctx, &bankpb.TransferRequest{
			FromAccount: req.FromAccount,
			ToAccount:   req.ToAccount,
			Amount:      amount,
			Description: req.Description,
		})
		if err != nil {
			st, _ := status.FromError(err)
			switch st.Code() {
			case 0:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "unknown error"})
			default:
				writeGRPCError(c, err)
			}
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"from_account":     transferResp.FromAccount,
			"to_account":       transferResp.ToAccount,
			"initial_amount":   transferResp.InitialAmount,
			"final_amount":     transferResp.FinalAmount,
			"fee":              transferResp.Fee,
			"currency":         transferResp.Currency,
			"payment_code":     transferResp.PaymentCode,
			"reference_number": transferResp.ReferenceNumber,
			"purpose":          transferResp.Purpose,
			"status":           transferResp.Status,
			"timestamp":        transferResp.Timestamp,
		})
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
