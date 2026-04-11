package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"
)

func (s *Server) CreateAccount(c *gin.Context) {
	var req createAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	employeeID, ok := s.getAuthenticatedEmployeeID(c)
	if !ok {
		return
	}

	// TEKUCI -> checking, DEVIZNI -> foreign
	var accountType string
	var currency string
	var maintainanceCost float64
	switch strings.ToUpper(req.AccountType) {
	case "TEKUCI":
		accountType = "checking"
		currency = "RSD"
		maintainanceCost = 25500
	case "DEVIZNI":
		accountType = "foreign"
		currency = req.Currency
		maintainanceCost = 0
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "account_type must be TEKUCI or DEVIZNI"})
		return
	}

	var ownerType string
	subtypeLower := strings.ToLower(req.Subtype)
	if strings.Contains(subtypeLower, "business") || strings.Contains(subtypeLower, "poslovni") {
		ownerType = "business"
	} else {
		ownerType = "personal"
	}

	name := fmt.Sprintf("%s-%s", accountType, req.Subtype)

	validUntil := time.Now().AddDate(5, 0, 0).Unix()

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.CreateAccount(ctx, &bankpb.CreateAccountRequest{
		Name:             name,
		Owner:            req.ClientID,
		Currency:         currency,
		OwnerType:        ownerType,
		AccountType:      accountType,
		MaintainanceCost: maintainanceCost,
		DailyLimit:       req.DailyLimit,
		MonthlyLimit:     req.MonthlyLimit,
		CreatedBy:        employeeID,
		ValidUntil:       validUntil,
		CreateCard:       req.CreateCard,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	if !resp.Valid {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": resp.Error})
		return
	}

	detailResp, err := s.BankClient.GetAccountDetails(ctx, &bankpb.GetAccountDetailsRequest{
		AccountNumber: resp.AccountNumber,
	})
	if err != nil {
		c.JSON(http.StatusCreated, gin.H{
			"account_number": resp.AccountNumber,
		})
		return
	}

	c.JSON(http.StatusCreated, accountResponse(detailResp.Account))
}

func accountResponse(a *bankpb.Account) gin.H {
	return gin.H{
		"account_number":    a.AccountNumber,
		"account_name":      a.AccountName,
		"owner_id":          a.OwnerId,
		"balance":           a.Balance,
		"available_balance": a.AvailableBalance,
		"employee_id":       a.EmployeeId,
		"creation_date":     time.Unix(a.CreationDate, 0).Format(time.RFC3339),
		"expiration_date":   time.Unix(a.ExpirationDate, 0).Format(time.RFC3339),
		"currency":          a.Currency,
		"status":            a.Status,
		"account_type":      a.AccountType,
		"daily_limit":       a.DailyLimit,
		"monthly_limit":     a.MonthlyLimit,
		"daily_spending":    a.DailySpending,
		"monthly_spending":  a.MonthlySpending,
	}
}

func (s *Server) GetAccounts(c *gin.Context) {
	var query getAccountsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	resp, err := s.BankClient.ListAccounts(ctx, &bankpb.ListAccountsRequest{
		FirstName:     query.FirstName,
		LastName:      query.LastName,
		AccountNumber: query.AccountNumber,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	accounts := make([]gin.H, 0, len(resp.Accounts))
	for _, a := range resp.Accounts {
		accounts = append(accounts, accountResponse(a))
	}

	c.JSON(http.StatusOK, accounts)
}

func (s *Server) GetAccountByNumber(c *gin.Context) {
	var uri accountNumberURI
	if err := c.ShouldBindUri(&uri); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	resp, err := s.BankClient.GetAccountDetails(ctx, &bankpb.GetAccountDetailsRequest{
		AccountNumber: uri.AccountNumber,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, accountResponse(resp.Account))
}

func (s *Server) UpdateAccountName(c *gin.Context) {
	var uri accountNumberURI
	if err := c.ShouldBindUri(&uri); err != nil {
		writeBindError(c, err)
		return
	}

	var req updateAccountNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	_, err := s.BankClient.UpdateAccountName(ctx, &bankpb.UpdateAccountNameRequest{
		AccountNumber: uri.AccountNumber,
		Name:          req.Name,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Name updated"})
}

func (s *Server) UpdateAccountLimits(c *gin.Context) {
	var uri accountNumberURI
	if err := c.ShouldBindUri(&uri); err != nil {
		writeBindError(c, err)
		return
	}

	var req updateAccountLimitsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
	))

	_, err := s.BankClient.UpdateAccountLimits(ctx, &bankpb.UpdateAccountLimitsRequest{
		AccountNumber: uri.AccountNumber,
		DailyLimit:    req.DailyLimit,
		MonthlyLimit:  req.MonthlyLimit,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Limits updated"})
}
