package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/exchange"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
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
		api.POST("/totp/setup/begin", AuthenticatedMiddleware(server.UserClient), server.TOTPSetupBegin)
		api.POST("/totp/setup/confirm", AuthenticatedMiddleware(server.UserClient), server.TOTPSetupConfirm)
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
		transactions.GET("/:id", server.GetTransactionByID)         //TODO visak, stvari koje nisu u api spec
		transactions.GET("/:id/pdf", server.GenerateTransactionPDF) //TODO visak, stvari koje nisu u api spec
		transactions.POST("/payment", server.PayoutMoneyToOtherAccount)
		transactions.POST("/transfer", server.TransferMoneyBetweenAccounts)
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

	employees := api.Group("/employees", AuthenticatedMiddleware(server.UserClient))
	{
		employees.POST("", server.CreateEmployeeAccount)
		employees.GET("/:employeeId", server.GetEmployeeByID)
		employees.DELETE("/:employeeId", server.DeleteEmployeeByID)
		employees.GET("", server.GetEmployees)
		employees.PATCH("/:employeeId", server.UpdateEmployee)
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
		accounts.PATCH("/:accountNumber/limit", TOTPMiddleware(server.TOTPClient), server.UpdateAccountLimits)
	}

	loans := api.Group("/loans", AuthenticatedMiddleware(server.UserClient))
	{
		loans.GET("", server.GetLoans)
		loans.GET("/:loanNumber", server.GetLoanByNumber)
	}

	loanRequests := api.Group("/loan-requests", AuthenticatedMiddleware(server.UserClient))
	{
		loanRequests.POST("", server.CreateLoanRequest)
		loanRequests.GET("", server.GetLoanRequests)
		loanRequests.PATCH("/:id/approve", server.ApproveLoanRequest)
		loanRequests.PATCH("/:id/reject", server.RejectLoanRequest)
	}

	cards := api.Group("/cards")
	{
		cards.GET("", AuthenticatedMiddleware(server.UserClient), server.GetCards)
		cards.POST("", AuthenticatedMiddleware(server.UserClient), server.RequestCard)
		cards.GET("/confirm", server.ConfirmCard) //TODO visak, stvari koje nisu u api spec
		cards.PATCH("/:cardNumber/block", AuthenticatedMiddleware(server.UserClient), server.BlockCard)
	}

	api.GET("/exchange-rates", AuthenticatedMiddleware(server.UserClient), server.GetExchangeRates)

	exchange := api.Group("/exchange")
	{
		exchange.POST("/convert", server.ConvertMoney) //TODO visak, stvari koje nisu u api spec
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

	c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
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

func (s *Server) getAuthenticatedEmployeeID(c *gin.Context) (int64, bool) {
	email := strings.TrimSpace(c.GetString("email"))
	if email == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "authentication required",
		})
		return 0, false
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.GetEmployeeByEmail(ctx, &userpb.GetEmployeeByEmailRequest{
		Email: email,
	})
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "authenticated user is not an employee",
		})
		return 0, false
	}

	return resp.Id, true
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

	// Look up permissions: employee gets permissions from profile, client gets empty array
	var permissions []string
	empResp, empErr := s.UserClient.GetEmployeeByEmail(ctx, &userpb.GetEmployeeByEmailRequest{
		Email: req.Email,
	})
	if empErr == nil {
		permissions = empResp.Permissions
	}
	if permissions == nil {
		permissions = []string{}
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken":  resp.AccessToken,
		"refreshToken": resp.RefreshToken,
		"permissions":  permissions,
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

	c.JSON(http.StatusOK, clients)
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

	if !resp.Valid {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": resp.Response})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":            uri.ClientID,
		"first_name":    req.FirstName,
		"last_name":     req.LastName,
		"date_of_birth": req.DateOfBirth,
		"gender":        req.Gender,
		"email":         req.Email,
		"phone_number":  req.PhoneNumber,
		"address":       req.Address,
	})
}

func (s *Server) CreateEmployeeAccount(c *gin.Context) {
	var req createEmployeeAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	// Parse birth_date string to unix timestamp for proto
	var birthDateUnix int64
	if req.BirthDate != "" {
		t, err := time.Parse(time.DateOnly, req.BirthDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid birth_date format, expected YYYY-MM-DD"})
			return
		}
		birthDateUnix = t.Unix()
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.CreateEmployeeAccount(ctx, &userpb.CreateEmployeeRequest{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		BirthDate:   birthDateUnix,
		Gender:      req.Gender,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		Address:     req.Address,
		Username:    req.Username,
		Position:    req.Position,
		Department:  req.Department,
		Password:    req.Password,
		Active:      req.Active,
		Permissions: req.Permissions,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	perms := resp.Permissions
	if perms == nil {
		perms = []string{}
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":          resp.Id,
		"first_name":  resp.FirstName,
		"last_name":   resp.LastName,
		"email":       resp.Email,
		"position":    resp.Position,
		"phone":       resp.PhoneNumber,
		"active":      resp.Active,
		"birth_date":  time.Unix(resp.BirthDate, 0).Format(time.DateOnly),
		"gender":      resp.Gender,
		"address":     resp.Address,
		"username":    resp.Username,
		"department":  resp.Department,
		"permissions": perms,
	})
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

	c.JSON(http.StatusOK, companies)
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

	employeeID, ok := s.getAuthenticatedEmployeeID(c)
	if !ok {
		return
	}

	// TEKUCI -> checking, DEVIZNI -> foreign
	var accountType string
	var currency string
	var maintainanceCost int64
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
		DailyLimit:       int64(req.DailyLimit),
		MonthlyLimit:     int64(req.MonthlyLimit),
		CreatedBy:        employeeID,
		ValidUntil:       validUntil,
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

	perms := resp.Permissions
	if perms == nil {
		perms = []string{}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          resp.Id,
		"first_name":  resp.FirstName,
		"last_name":   resp.LastName,
		"birth_date":  time.Unix(resp.BirthDate, 0).Format(time.DateOnly),
		"gender":      resp.Gender,
		"email":       resp.Email,
		"phone":       resp.PhoneNumber,
		"address":     resp.Address,
		"username":    resp.Username,
		"position":    resp.Position,
		"department":  resp.Department,
		"active":      resp.Active,
		"permissions": perms,
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
		employees := make([]gin.H, 0, len(resp.Employees))
		for _, e := range resp.Employees {
			employees = append(employees, gin.H{
				"id":         e.Id,
				"first_name": e.FirstName,
				"last_name":  e.LastName,
				"email":      e.Email,
				"position":   e.Position,
				"phone":      e.PhoneNumber,
				"active":     e.Active,
			})
		}
		c.JSON(http.StatusOK, employees)
	} else {
		c.JSON(http.StatusOK, []gin.H{})
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

	resp, err := s.UserClient.UpdateEmployee(ctx, &userpb.UpdateEmployeeRequest{
		Id:          uri.EmployeeID,
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
		return
	}

	perms := resp.Permissions
	if perms == nil {
		perms = []string{}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          resp.Id,
		"first_name":  resp.FirstName,
		"last_name":   resp.LastName,
		"birth_date":  time.Unix(resp.BirthDate, 0).Format(time.DateOnly),
		"gender":      resp.Gender,
		"email":       resp.Email,
		"phone":       resp.PhoneNumber,
		"address":     resp.Address,
		"username":    resp.Username,
		"position":    resp.Position,
		"department":  resp.Department,
		"active":      resp.Active,
		"permissions": perms,
	})
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

func loanListResponse(resp *bankpb.GetLoansResponse) []gin.H {
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
	return loans
}

func (s *Server) GetLoans(c *gin.Context) {
	var query getLoansQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	email := c.GetString("email")

	// Try employee first; if not an employee, fall back to client view
	_, err := s.UserClient.GetEmployeeByEmail(ctx, &userpb.GetEmployeeByEmailRequest{Email: email})
	if err == nil {
		// Employee: get all loans
		resp, err := s.BankClient.GetAllLoans(ctx, &bankpb.GetAllLoansRequest{
			LoanType:      query.LoanType,
			AccountNumber: query.AccountNumber,
			Status:        query.Status,
		})
		if err != nil {
			writeGRPCError(c, err)
			return
		}
		c.JSON(http.StatusOK, loanListResponse(resp))
		return
	}

	// If the user service returned something other than NotFound, it's a real error
	if code := status.Code(err); code != codes.NotFound {
		writeGRPCError(c, err)
		return
	}

	// Client view
	resp, err := s.BankClient.GetLoans(ctx, &bankpb.GetLoansRequest{
		ClientEmail:   email,
		LoanType:      query.LoanType,
		AccountNumber: query.AccountNumber,
		Status:        query.Status,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, loanListResponse(resp))
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
		ClientEmail:      c.GetString("email"),
		AccountNumber:    req.AccountNumber,
		LoanType:         req.LoanType,
		Amount:           req.Amount,
		RepaymentPeriod:  req.RepaymentPeriod,
		Currency:         req.Currency,
		Purpose:          req.Purpose,
		Salary:           req.Salary,
		EmploymentStatus: req.EmploymentStatus,
		EmploymentPeriod: req.EmploymentPeriod,
		PhoneNumber:      req.PhoneNumber,
		InterestRateType: req.InterestRateType,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Status(http.StatusCreated)
}

func (s *Server) GetLoanRequests(c *gin.Context) {
	_, ok := s.getAuthenticatedEmployeeID(c)
	if !ok {
		return
	}

	var query getLoanRequestsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.BankClient.GetLoanRequests(ctx, &bankpb.GetLoanRequestsRequest{
		LoanType:      query.LoanType,
		AccountNumber: query.AccountNumber,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	requests := make([]gin.H, 0, len(resp.LoanRequests))
	for _, r := range resp.LoanRequests {
		requests = append(requests, gin.H{
			"id":              r.Id,
			"status":          r.Status,
			"loan_type":       r.LoanType,
			"loan_amount":     r.Amount,
			"purpose":         r.Purpose,
			"account_number":  r.AccountNumber,
			"submission_date": r.SubmissionDate,
		})
	}

	c.JSON(http.StatusOK, requests)
}

func (s *Server) ApproveLoanRequest(c *gin.Context) {
	_, ok := s.getAuthenticatedEmployeeID(c)
	if !ok {
		return
	}

	var uri loanRequestIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "loan request id is required and must be a valid integer")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	_, err := s.BankClient.ApproveLoanRequest(ctx, &bankpb.ApproveLoanRequestRequest{
		Id: uri.ID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Status(http.StatusOK)
}

func (s *Server) RejectLoanRequest(c *gin.Context) {
	_, ok := s.getAuthenticatedEmployeeID(c)
	if !ok {
		return
	}

	var uri loanRequestIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "loan request id is required and must be a valid integer")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	_, err := s.BankClient.RejectLoanRequest(ctx, &bankpb.RejectLoanRequestRequest{
		Id: uri.ID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Status(http.StatusOK)
}

func (s *Server) GetCards(c *gin.Context) {
	email, exists := c.Get("email")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user email not found in token"})
		return
	}

	md := metadata.Pairs("user-email", email.(string))
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

	_, err := s.BankClient.RequestCard(ctx, &bankpb.RequestCardRequest{
		AccountNumber: req.AccountNumber,
		CardType:      req.CardType,
	})

	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"account_number": req.AccountNumber,
		"card_type":      req.CardType,
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
	var uri blockCardURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "card number is required")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
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

	c.JSON(http.StatusOK, recipients)
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
		"id":             resp.Recipient.Id,
		"name":           resp.Recipient.Name,
		"account_number": resp.Recipient.AccountNumber,
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

	_, err := s.BankClient.UpdatePaymentRecipient(ctx, &bankpb.UpdatePaymentRecipientRequest{
		Id:            uri.ID,
		ClientId:      clientID,
		Name:          req.Name,
		AccountNumber: req.AccountNumber,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Recipient updated"})
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

	_, err := s.BankClient.DeletePaymentRecipient(ctx, &bankpb.DeletePaymentRecipientRequest{
		Id:       uri.ID,
		ClientId: clientID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

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

func (s *Server) GetExchangeRates(c *gin.Context) {
	resp, err := s.ExchangeClinet.GetExchangeRates(c.Request.Context(), &exchangepb.ExchangeRateListRequest{})
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

func (s *Server) TOTPSetupBegin(c *gin.Context) {
	key, keyPresent := c.Get("email")
	if !keyPresent {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user email not found in token"})
		return
	}
	email, ok := key.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user email not found in token"})
		return
	}
	resp, err := s.TOTPClient.EnrollBegin(context.Background(), &userpb.EnrollBeginRequest{
		Email: email,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"url": resp.Url,
	})
}

func (s *Server) TOTPSetupConfirm(c *gin.Context) {
	var req TOTPSetupConfirmRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	key, keyPresent := c.Get("email")
	if !keyPresent {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user email not found in token"})
		return
	}
	email, ok := key.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user email not found in token"})
		return
	}
	resp, err := s.TOTPClient.EnrollConfirm(context.Background(), &userpb.EnrollConfirmRequest{
		Email: email,
		Code:  req.Code,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	if resp.Success {
		c.Status(200)
	} else {
		c.JSON(http.StatusUnauthorized, gin.H{
			"message": "wrong code",
		})
	}
}
