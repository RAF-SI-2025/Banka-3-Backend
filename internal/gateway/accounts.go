package gateway

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"
)

func (s *Server) CreateAccount(c *gin.Context) {
	var req CreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	var employeeID, ok = s.getAuthenticatedEmployeeID(c)
	if !ok {
		return
	}

	// TEKUCI -> checking, DEVIZNI -> foreign
	var accountType string
	var currency string
	switch strings.ToUpper(req.AccountType) {
	case "TEKUCI":
		accountType = "checking"
		currency = "RSD"
	case "DEVIZNI":
		accountType = "foreign"
		currency = req.Currency
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "account_type must be TEKUCI or DEVIZNI"})
		return
	}

	var pbBusinessInfo *bankpb.BusinessInfo
	if req.BusinessInfo != nil {
		pbBusinessInfo = &bankpb.BusinessInfo{
			CompanyName:        req.BusinessInfo.CompanyName,
			RegistrationNumber: req.BusinessInfo.RegistrationNumber,
			Pib:                req.BusinessInfo.PIB,
			ActivityCode:       req.BusinessInfo.ActivityCode,
			Address:            req.BusinessInfo.Address,
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"user-email", c.GetString("email"),
		"employee-id", strconv.FormatInt(employeeID, 10),
	))

	resp, err := s.BankClient.CreateAccount(ctx, &bankpb.CreateAccountRequest{
		ClientId:       req.ClientID,
		Name:           req.Name,
		AccountType:    accountType,
		Subtype:        req.Subtype,
		Currency:       currency,
		InitialBalance: req.InitialBalance,
		DailyLimit:     req.DailyLimit,
		MonthlyLimit:   req.MonthlyLimit,
		CreateCard:     req.CreateCard,
		CardType:       req.CardType,
		CardBrand:      req.CardBrand,
		BusinessInfo:   pbBusinessInfo,
	})

	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusCreated, resp)
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
