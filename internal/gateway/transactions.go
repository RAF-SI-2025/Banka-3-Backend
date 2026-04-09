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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	resp, err := s.BankClient.GetTransactions(ctx, &bankpb.GetTransactionsRequest{
		AccountNumber: query.AccountNumber,
		DateFrom:      query.DateFrom,
		DateTo:        query.DateTo,
		AmountFrom:    query.AmountFrom,
		AmountTo:      query.AmountTo,
		Status:        query.Status,
		Page:          query.Page,
		PageSize:      query.PageSize,
		SortBy:        query.SortBy,
		SortOrder:     query.SortOrder,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"transactions": resp.Transactions,
		"page":         resp.Page,
		"page_size":    resp.PageSize,
		"total":        resp.Total,
		"total_pages":  resp.TotalPages,
	})
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	resp, err := s.BankClient.GetTransactionById(ctx, &bankpb.GetTransactionByIdRequest{
		Id:   uri.ID,
		Type: query.Type,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, resp.Transaction)
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	resp, err := s.BankClient.GenerateTransactionPdf(ctx, &bankpb.GenerateTransactionPdfRequest{
		Id:   uri.ID,
		Type: query.Type,
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
	res, err := s.BankClient.PayoutMoneyToOtherAccount(context.Background(), &bankpb.PaymentRequest{
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

	res, err := s.BankClient.TransferMoneyBetweenAccounts(context.Background(), &bankpb.TransferRequest{
		FromAccount: req.FromAccount,
		ToAccount:   req.ToAccount,
		Amount:      req.Amount,
		Description: req.Description,
	})

	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			switch st.Code() {
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
