package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (s *Server) GetTransactions(c *gin.Context) {
	var query getTransactionsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	email := c.GetString("email")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", email,
	))

	resp, err := s.BankClient.ListClientTransactions(ctx, &bankpb.ListClientTranasctionsRequest{
		AccountNumber: query.AccountNumber,
		Date:          query.Date,
		Amount:        int64(query.Amount),
		Status:        query.Status,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	transactions := make([]gin.H, 0, len(resp.Transactions))
	for _, t := range resp.Transactions {
		transactions = append(transactions, gin.H{
			"from_account":     t.FromAccount,
			"to_account":       t.ToAccount,
			"initial_amount":   t.InitialAmount,
			"final_amount":     t.FinalAmount,
			"fee":              t.Fee,
			"currency":         t.Currency,
			"payment_code":     t.PaymentCode,
			"reference_number": t.ReferenceNumber,
			"purpose":          t.Purpose,
			"status":           t.Status,
			"timestamp":        time.Unix(t.Timestamp, 0).Format(time.RFC3339),
		})
	}

	c.JSON(http.StatusOK, transactions)
}

func (s *Server) GetTransactionByID(c *gin.Context) {
	var uri transactionByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		writeBindError(c, err)
		return
	}
	var query transactionTypeQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	clientID, ok := s.getAuthenticatedClientID(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.GetTransactionById(ctx, &bankpb.GetTransactionByIdRequest{
		ClientId: clientID,
		Id:       uri.ID,
		Type:     query.Type,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	t := resp.Transaction
	c.JSON(http.StatusOK, gin.H{
		"id":                t.Id,
		"type":              t.Type,
		"from_account":      t.FromAccount,
		"to_account":        t.ToAccount,
		"start_amount":      t.StartAmount,
		"end_amount":        t.EndAmount,
		"commission":        t.Commission,
		"status":            t.Status,
		"timestamp":         t.Timestamp,
		"recipient_id":      t.RecipientId,
		"transaction_code":  t.TransactionCode,
		"call_number":       t.CallNumber,
		"reason":            t.Reason,
		"start_currency_id": t.StartCurrencyId,
		"exchange_rate":     t.ExchangeRate,
	})
}

func (s *Server) GenerateTransactionPDF(c *gin.Context) {
	var uri transactionByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		writeBindError(c, err)
		return
	}
	var query transactionTypeQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	clientID, ok := s.getAuthenticatedClientID(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	resp, err := s.BankClient.GenerateTransactionPdf(ctx, &bankpb.GenerateTransactionPdfRequest{
		ClientId: clientID,
		Id:       uri.ID,
		Type:     query.Type,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, resp.FileName))
	c.Data(http.StatusOK, "application/pdf", resp.Pdf)
}

func (s *Server) PayoutMoneyToOtherAccount(c *gin.Context) {
	var req paymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	println(c.Request)

	paymentCodeParsed, err := strconv.ParseInt(req.PaymentCode, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid payment_code",
		})
		return
	}
	if req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "amount must be greater than zero",
		})
		return
	}

	if req.SenderAccount == req.RecipientAccount {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "sender and recipient account must not be the same account",
		})
		return
	}
	email := c.GetString("email")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("user-email", email))
	res, err := s.BankClient.PayoutMoneyToOtherAccount(ctx, &bankpb.PaymentRequest{
		SenderAccount:    req.SenderAccount,
		RecipientAccount: req.RecipientAccount,
		RecipientName:    req.RecipientName,
		Amount:           req.Amount,
		PaymentCode:      paymentCodeParsed,
		ReferenceNumber:  req.ReferenceNumber,
		Purpose:          req.Purpose,
	})
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			switch st.Code() {

			case codes.Unauthenticated:
				c.JSON(http.StatusUnauthorized, gin.H{"error": st.Message()})

			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{
					"error": st.Message(),
				})

			case codes.FailedPrecondition:
				c.JSON(http.StatusBadRequest, gin.H{
					"error": st.Message(),
				})

			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{
					"error": st.Message(),
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}
		// fallback if it's not a gRPC status error
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "unknown error",
		})
		return
	}

	c.JSON(http.StatusOK, res)
}

func (s *Server) TransferMoneyBetweenAccounts(c *gin.Context) {
	var req transferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	if req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "amount must be greater than zero"})
		return
	}

	if req.FromAccount == req.ToAccount {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sender and recipient account must not be the same account"})
		return
	}

	email := c.GetString("email")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", email,
	))

	res, err := s.BankClient.TransferMoneyBetweenAccounts(ctx, &bankpb.TransferRequest{
		FromAccount: req.FromAccount,
		ToAccount:   req.ToAccount,
		Amount:      req.Amount,
		Description: req.Description,
	})

	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			switch st.Code() {
			case codes.Unauthenticated:
				c.JSON(http.StatusUnauthorized, gin.H{"error": st.Message()})
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
			case codes.FailedPrecondition, codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			}
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unknown error"})
		return
	}

	c.JSON(http.StatusOK, res)
}

func (s *Server) GetTransactionsHistoryForUserEmail(c *gin.Context) {
	var params getTransfersHistoryQuery
	if err := c.ShouldBindQuery(&params); err != nil {
		writeBindError(c, err)
		return
	}
	res, err := s.BankClient.GetTransfersHistoryForUserEmail(
		c,
		&bankpb.TransferHistoryRequest{
			Email:    params.Email,
			Page:     params.Page,
			PageSize: params.PageSize,
		},
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}
