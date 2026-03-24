package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/exchange"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
)

func SetupApi(router *gin.Engine, server *Server) {
	router.GET("/healthz", server.Healthz)
	router.Use(CORSMiddleware())
	api := router.Group("/api")
	{
		api.POST("/login", server.Login)
		api.POST("/logout", AuthenticatedMiddleware(server.UserClient), server.Logout)
		api.POST("/token/refresh", server.Refresh)
	}

	recipients := api.Group("/recipients", AuthenticatedMiddleware(server.UserClient))
	{
		recipients.GET("", server.GetPaymentRecipients)
		recipients.POST("", server.CreatePaymentRecipient)
		recipients.PUT("/:id", server.UpdatePaymentRecipient)
		recipients.DELETE("/:id", server.DeletePaymentRecipient)
	}

	transactions := api.Group("/transactions", AuthenticatedMiddleware(server.UserClient))
	{
		transactions.GET("", server.GetTransactions)
		transactions.GET("/:id", server.GetTransactionByID)
		transactions.GET("/:id/pdf", server.GenerateTransactionPDF)
		transactions.POST("/payments", server.PayoutMoneyToOtherAccount)
		transactions.POST("/transfers", server.TransferMoneyBetweenAccounts)
	}

	passwordReset := api.Group("/password-reset")
	{
		passwordReset.POST("/request", server.RequestPasswordReset)
		passwordReset.POST("/confirm", server.ConfirmPasswordReset)
	}

	clients := api.Group("/clients")
	{
		clients.POST("", server.CreateClientAccount)
		clients.GET("", server.GetClients)
		clients.PUT("/:id", server.UpdateClient)
	}

	employees := api.Group("/employees")
	{
		employees.POST("", server.CreateEmployeeAccount)
		employees.GET("/:id", server.GetEmployeeByID)
		employees.DELETE("/:id", server.DeleteEmployeeByID)
		employees.GET("", server.GetEmployees)
		employees.PUT("/:id", server.UpdateEmployee)
	}

	companies := api.Group("/companies")
	{
		companies.POST("", server.CreateCompany)
		companies.GET("", server.GetCompanies)
		companies.GET("/:id", server.GetCompanyByID)
		companies.PUT("/:id", server.UpdateCompany)
	}

	accounts := api.Group("/accounts", AuthenticatedMiddleware(server.UserClient))
	{
		accounts.POST("", server.CreateAccount)
		accounts.GET("", server.GetAccounts)
		accounts.GET("/:accountNumber", server.GetAccountByNumber)
		accounts.PATCH("/:accountNumber/name", server.UpdateAccountName)
		accounts.PATCH("/:accountNumber/limit", server.UpdateAccountLimits)
	}

	loans := api.Group("/loans", AuthenticatedMiddleware(server.UserClient))
	{
		loans.GET("", server.GetLoans)
		loans.GET("/:loanNumber", server.GetLoanByNumber)
	}

	api.POST("/loan-requests", AuthenticatedMiddleware(server.UserClient), server.CreateLoanRequest)

	cards := api.Group("/cards")
	{
		cards.GET("", AuthenticatedMiddleware(server.UserClient), server.GetCards)
		cards.POST("/request", AuthenticatedMiddleware(server.UserClient), server.RequestCard)
		cards.GET("/confirm", server.ConfirmCard)
		cards.PATCH("/:id/block", AuthenticatedMiddleware(server.UserClient), server.BlockCard)
	}

	exchange := api.Group("/exchange")
	{
		exchange.GET("/rates", server.GetExchangeRates)
		exchange.POST("/convert", server.ConvertMoney)
	}
}

func (s *Server) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) Logout(c *gin.Context) {
	email := c.GetString("email")
	println(email)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	_, err := s.UserClient.Logout(ctx, &userpb.LogoutRequest{
		Email: email,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Status(http.StatusAccepted)
}

func (s *Server) getAuthenticatedClientID(c *gin.Context) (int64, bool) {
	email := strings.TrimSpace(c.GetString("email"))
	if email == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "authentication required",
		})
		return 0, false
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.GetClients(ctx, &userpb.GetClientsRequest{
		Email: email,
	})
	if err != nil {
		writeGRPCError(c, err)
		return 0, false
	}

	if len(resp.Clients) == 0 {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "authenticated user is not a client",
		})
		return 0, false
	}

	return resp.Clients[0].Id, true
}

func (s *Server) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.Login(ctx, &userpb.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  resp.AccessToken,
		"refresh_token": resp.RefreshToken,
	})
}

func (s *Server) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.Refresh(ctx, &userpb.RefreshRequest{
		RefreshToken: req.RefreshToken,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  resp.AccessToken,
		"refresh_token": resp.RefreshToken,
	})
}

func (s *Server) CreateClientAccount(c *gin.Context) {
	var req createClientAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.CreateClientAccount(ctx, &userpb.CreateClientRequest{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		BirthDate:   req.DateOfBirth,
		Gender:      req.Gender,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		Address:     req.Address,
		Password:    req.Password,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	if resp.Valid {
		c.JSON(http.StatusCreated, gin.H{
			"valid": true,
		})
		return
	}

	c.JSON(http.StatusUnprocessableEntity, gin.H{
		"valid": false,
	})
}

func clientResponse(client *userpb.Client) gin.H {
	return gin.H{
		"id":            client.Id,
		"first_name":    client.FirstName,
		"last_name":     client.LastName,
		"date_of_birth": client.DateOfBirth,
		"gender":        client.Gender,
		"email":         client.Email,
		"phone_number":  client.PhoneNumber,
		"address":       client.Address,
	}
}

func (s *Server) GetClients(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.GetClients(ctx, &userpb.GetClientsRequest{
		FirstName: c.Query("first_name"),
		LastName:  c.Query("last_name"),
		Email:     c.Query("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	clients := make([]gin.H, 0, len(resp.Clients))
	for _, client := range resp.Clients {
		clients = append(clients, clientResponse(client))
	}

	c.JSON(http.StatusOK, gin.H{
		"clients": clients,
	})
}

func (s *Server) UpdateClient(c *gin.Context) {
	var uri clientByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "client id is required and must be a valid integer")
		return
	}

	var req updateClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.UpdateClient(ctx, &userpb.UpdateClientRequest{
		Id:          uri.ClientID,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		Gender:      req.Gender,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		Address:     req.Address,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) CreateEmployeeAccount(c *gin.Context) {
	var req createEmployeeAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.CreateEmployeeAccount(ctx, &userpb.CreateEmployeeRequest{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		BirthDate:   req.BirthDate,
		Gender:      req.Gender,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		Address:     req.Address,
		Username:    req.Username,
		Position:    req.Position,
		Department:  req.Department,
		Password:    req.Password,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusCreated, resp)
}

func companyResponse(company *bankpb.Company) gin.H {
	return gin.H{
		"id":               company.Id,
		"registered_id":    company.RegisteredId,
		"name":             company.Name,
		"tax_code":         company.TaxCode,
		"activity_code_id": company.ActivityCodeId,
		"address":          company.Address,
		"owner_id":         company.OwnerId,
	}
}

func (s *Server) CreateCompany(c *gin.Context) {
	var req createCompanyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.CreateCompany(ctx, &bankpb.CreateCompanyRequest{
		RegisteredId:   req.RegisteredID,
		Name:           req.Name,
		TaxCode:        req.TaxCode,
		ActivityCodeId: req.ActivityCodeID,
		Address:        req.Address,
		OwnerId:        req.OwnerID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusCreated, companyResponse(resp.Company))
}

func (s *Server) GetCompanyByID(c *gin.Context) {
	var uri companyByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "company id is required and must be a valid integer")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.GetCompanyById(ctx, &bankpb.GetCompanyByIdRequest{
		Id: uri.CompanyID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, companyResponse(resp.Company))
}

func (s *Server) GetCompanies(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.GetCompanies(ctx, &bankpb.GetCompaniesRequest{})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	companies := make([]gin.H, 0)
	for _, company := range resp.Companies {
		companies = append(companies, companyResponse(company))
	}

	c.JSON(http.StatusOK, gin.H{
		"companies": companies,
	})
}

func (s *Server) UpdateCompany(c *gin.Context) {
	var uri companyByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "company id is required and must be a valid integer")
		return
	}

	var req updateCompanyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.UpdateCompany(ctx, &bankpb.UpdateCompanyRequest{
		Id:             uri.CompanyID,
		Name:           req.Name,
		ActivityCodeId: req.ActivityCodeID,
		Address:        req.Address,
		OwnerId:        req.OwnerID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, companyResponse(resp.Company))
}

func (s *Server) CreateAccount(c *gin.Context) {
	var req createAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.CreateAccount(ctx, &bankpb.CreateAccountRequest{
		Name:             req.Name,
		Owner:            req.Owner,
		Currency:         req.Currency,
		OwnerType:        req.OwnerType,
		AccountType:      req.AccountType,
		MaintainanceCost: req.MaintainanceCost,
		DailyLimit:       req.DailyLimit,
		MonthlyLimit:     req.MonthlyLimit,
		CreatedBy:        req.CreatedBy,
		ValidUntil:       req.ValidUntil,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"valid":          resp.Valid,
		"account_number": resp.AccountNumber,
		"error":          resp.Error,
	})
}

func (s *Server) GetEmployeeByID(c *gin.Context) {
	var uri getEmployeeByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "employee id is required and must be a valid integer")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.GetEmployeeById(ctx, &userpb.GetEmployeeByIdRequest{
		Id: uri.EmployeeID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":           resp.Id,
		"first_name":   resp.FirstName,
		"last_name":    resp.LastName,
		"birth_date":   time.Unix(resp.BirthDate, 0).Format(time.DateOnly),
		"gender":       resp.Gender,
		"email":        resp.Email,
		"phone_number": resp.PhoneNumber,
		"address":      resp.Address,
		"username":     resp.Username,
		"position":     resp.Position,
		"department":   resp.Department,
		"active":       resp.Active,
		"permissions":  resp.Permissions,
	})
}

func (s *Server) DeleteEmployeeByID(c *gin.Context) {
	var uri getEmployeeByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "employee id is required and must be a valid integer")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	_, err := s.UserClient.DeleteEmployee(ctx, &userpb.DeleteEmployeeRequest{
		Id: uri.EmployeeID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) GetEmployees(c *gin.Context) {
	var query getEmployeesQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.GetEmployees(ctx, &userpb.GetEmployeesRequest{
		FirstName: query.FirstName,
		LastName:  query.LastName,
		Email:     query.Email,
		Position:  query.Position,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	if resp.Employees != nil {
		c.JSON(http.StatusOK, resp.Employees)
	} else {
		empty := make([]string, 0)
		c.JSON(http.StatusOK, empty)
	}
}

func (s *Server) UpdateEmployee(c *gin.Context) {
	var uri updateEmployeeURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "employee id is required and must be a valid integer")
		return
	}

	var req updateEmployeeRequest
	if err := c.BindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	println("please")
	resp, err := s.UserClient.UpdateEmployee(ctx, &userpb.UpdateEmployeeRequest{
		Id:          uri.EmployeeID,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		Gender:      req.Gender,
		PhoneNumber: req.PhoneNumber,
		Address:     req.Address,
		Position:    req.Position,
		Department:  req.Department,
		Active:      req.Active,
		Permissions: req.Permissions,
	})
	if err != nil {
		writeGRPCError(c, err)
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) RequestPasswordReset(c *gin.Context) {
	var req passwordResetRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	_, err := s.UserClient.RequestPasswordReset(ctx, &userpb.PasswordActionRequest{
		Email: req.Email,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "If that email exists, a reset link was sent.",
	})
}

func (s *Server) ConfirmPasswordReset(c *gin.Context) {
	var req passwordResetConfirmationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	resp, err := s.UserClient.SetPasswordWithToken(ctx, &userpb.SetPasswordWithTokenRequest{
		Token:       req.Token,
		NewPassword: req.NewPassword,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	if resp.Successful {
		c.Status(http.StatusOK)
	} else {
		c.Status(http.StatusUnprocessableEntity)
	}
}
func (s *Server) PayoutMoneyToOtherAccount(c *gin.Context) {
	var req paymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	c.Status(http.StatusNotImplemented)
}
func (s *Server) TransferMoneyBetweenAccounts(c *gin.Context) {
	var req transferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	c.Status(http.StatusNotImplemented)
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

	accounts := resp.Accounts
	if accounts == nil {
		accounts = []*bankpb.Account{}
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

	c.JSON(http.StatusOK, resp)
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

func (s *Server) GetLoans(c *gin.Context) {
	var query getLoansQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.GetLoans(ctx, &bankpb.GetLoansRequest{
		ClientEmail:   c.GetString("email"),
		LoanType:      query.LoanType,
		AccountNumber: query.AccountNumber,
		Status:        query.Status,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	loans := make([]gin.H, 0, len(resp.Loans))
	for _, loan := range resp.Loans {
		loans = append(loans, gin.H{
			"loan_number":             loan.LoanNumber,
			"loan_type":               loan.LoanType,
			"account_number":          loan.AccountNumber,
			"loan_amount":             loan.LoanAmount,
			"repayment_period":        loan.RepaymentPeriod,
			"nominal_rate":            loan.NominalRate,
			"effective_rate":          loan.EffectiveRate,
			"agreement_date":          loan.AgreementDate,
			"maturity_date":           loan.MaturityDate,
			"next_installment_amount": loan.NextInstallmentAmount,
			"next_installment_date":   loan.NextInstallmentDate,
			"remaining_debt":          loan.RemainingDebt,
			"currency":                loan.Currency,
			"status":                  loan.Status,
		})
	}

	c.JSON(http.StatusOK, loans)
}

func (s *Server) GetLoanByNumber(c *gin.Context) {
	var uri getLoanByNumberURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "loan number is required")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.GetLoanByNumber(ctx, &bankpb.GetLoanByNumberRequest{
		ClientEmail: c.GetString("email"),
		LoanNumber:  uri.LoanNumber,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"loan_number":             resp.LoanNumber,
		"loan_type":               resp.LoanType,
		"account_number":          resp.AccountNumber,
		"loan_amount":             resp.LoanAmount,
		"repayment_period":        resp.RepaymentPeriod,
		"nominal_rate":            resp.NominalRate,
		"effective_rate":          resp.EffectiveRate,
		"agreement_date":          resp.AgreementDate,
		"maturity_date":           resp.MaturityDate,
		"next_installment_amount": resp.NextInstallmentAmount,
		"next_installment_date":   resp.NextInstallmentDate,
		"remaining_debt":          resp.RemainingDebt,
		"currency":                resp.Currency,
		"status":                  resp.Status,
	})
}

func (s *Server) CreateLoanRequest(c *gin.Context) {
	var req createLoanRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	_, err := s.BankClient.CreateLoanRequest(ctx, &bankpb.CreateLoanRequestRequest{
		ClientEmail:     c.GetString("email"),
		AccountNumber:   req.AccountNumber,
		LoanType:        req.LoanType,
		Amount:          req.Amount,
		RepaymentPeriod: req.RepaymentPeriod,
		Currency:        req.Currency,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Status(http.StatusCreated)
}

func (s *Server) GetCards(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.GetCards(ctx, &bankpb.GetCardsRequest{})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, resp.Cards)
}

func (s *Server) RequestCard(c *gin.Context) {
	email, exists := c.Get("email")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user email not found in token"})
		return
	}

	var req requestCardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	md := metadata.Pairs("user-email", email.(string))
	ctx := metadata.NewOutgoingContext(c.Request.Context(), md)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.RequestCard(ctx, &bankpb.RequestCardRequest{
		AccountNumber: req.AccountNumber,
		CardType:      req.CardType,
		CardBrand:     req.CardBrand,
	})

	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusAccepted, resp)
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
	var uri blockCardURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "card id is required and must be a valid integer")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.BlockCard(ctx, &bankpb.BlockCardRequest{
		CardId: uri.CardID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) GetPaymentRecipients(c *gin.Context) {
	clientID, ok := s.getAuthenticatedClientID(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.GetPaymentRecipients(ctx, &bankpb.GetPaymentRecipientsRequest{
		ClientId: clientID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	recipients := make([]gin.H, 0)

	for _, r := range resp.Recipients {
		recipients = append(recipients, gin.H{
			"id":             r.Id,
			"name":           r.Name,
			"account_number": r.AccountNumber,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"recipients": recipients,
	})
}

func (s *Server) CreatePaymentRecipient(c *gin.Context) {
	var req createPaymentRecipientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	clientID, ok := s.getAuthenticatedClientID(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.CreatePaymentRecipient(ctx, &bankpb.CreatePaymentRecipientRequest{
		ClientId:      clientID,
		Name:          req.Name,
		AccountNumber: req.AccountNumber,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"recipient": gin.H{
			"id":             resp.Recipient.Id,
			"name":           resp.Recipient.Name,
			"account_number": resp.Recipient.AccountNumber,
		},
	})
}

func (s *Server) UpdatePaymentRecipient(c *gin.Context) {
	var uri paymentRecipientByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid recipient id",
		})
		return
	}

	var req updatePaymentRecipientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	clientID, ok := s.getAuthenticatedClientID(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.UpdatePaymentRecipient(ctx, &bankpb.UpdatePaymentRecipientRequest{
		Id:            uri.ID,
		ClientId:      clientID,
		Name:          req.Name,
		AccountNumber: req.AccountNumber,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"recipient": gin.H{
			"id":             resp.Recipient.Id,
			"name":           resp.Recipient.Name,
			"account_number": resp.Recipient.AccountNumber,
		},
	})
}

func (s *Server) DeletePaymentRecipient(c *gin.Context) {
	var uri paymentRecipientByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid recipient id",
		})
		return
	}

	clientID, ok := s.getAuthenticatedClientID(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.DeletePaymentRecipient(ctx, &bankpb.DeletePaymentRecipientRequest{
		Id:       uri.ID,
		ClientId: clientID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": resp.Success,
	})
}

func (s *Server) GetTransactions(c *gin.Context) {
	var query getTransactionsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	clientID, ok := s.getAuthenticatedClientID(c)
	if !ok {
		return
	}

	if query.Page <= 0 {
		query.Page = 1
	}
	if query.PageSize <= 0 {
		query.PageSize = 10
	}
	if query.SortBy == "" {
		query.SortBy = "timestamp"
	}
	if query.SortOrder == "" {
		query.SortOrder = "desc"
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.GetTransactions(ctx, &bankpb.GetTransactionsRequest{
		ClientId:   clientID,
		DateFrom:   query.DateFrom,
		DateTo:     query.DateTo,
		AmountFrom: query.AmountFrom,
		AmountTo:   query.AmountTo,
		Status:     query.Status,
		Page:       query.Page,
		PageSize:   query.PageSize,
		SortBy:     query.SortBy,
		SortOrder:  query.SortOrder,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	transactions := make([]gin.H, 0, len(resp.Transactions))
	for _, t := range resp.Transactions {
		transactions = append(transactions, gin.H{
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

	c.JSON(http.StatusOK, gin.H{
		"transactions": transactions,
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

func (s *Server) GetExchangeRates(c *gin.Context) {
	resp, err := s.ExchangeClinet.GetExchangeRates(c.Request.Context(), &exchangepb.ExchangeRateListRequest{})
	if err != nil {
		st, _ := status.FromError(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": st.Message()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) ConvertMoney(c *gin.Context) {
	var req conversionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := s.ExchangeClinet.ConvertMoney(c.Request.Context(), &exchangepb.ConversionRequest{
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
