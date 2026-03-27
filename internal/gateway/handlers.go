package gateway

import (
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func setupCors(router *gin.Engine) {
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173"},
		AllowMethods:     []string{"GET, POST, PUT, PATCH, DELETE, OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Authorization", "TOTP", "X-Requested-With"},
		ExposeHeaders:    []string{"Content-Length", "X-Custom-Header"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
}

func SetupApi(router *gin.Engine, server *Server) {
	router.GET("/healthz", server.Healthz)
	setupCors(router)
	api := router.Group("/api")

	secured := PermissionMiddleware(server.UserClient)
	auth := AuthenticatedMiddleware(server.UserClient)
	totp := TOTPMiddleware(server.TOTPClient)

	{
		api.POST("/login", server.Login)
		api.POST("/logout", auth, server.Logout)
		api.POST("/token/refresh", server.Refresh)

		t := api.Group("/totp", auth)
		t.POST("/setup/begin", server.TOTPSetupBegin)
		t.POST("/setup/confirm", server.TOTPSetupConfirm)
		t.GET("/status", server.TOTPStatus)
	}

	recipients := api.Group("/recipients", auth)
	{
		recipients.GET("", server.GetPaymentRecipients)
		recipients.POST("", server.CreatePaymentRecipient)
		recipients.PUT("/:id", server.UpdatePaymentRecipient)
		recipients.DELETE("/:id", server.DeletePaymentRecipient)
	}

	transactions := api.Group("/transactions", auth)
	{
		transactions.GET("", server.GetTransactions)
		transactions.GET("/:id", server.GetTransactionByID)         //TODO visak, stvari koje nisu u api spec
		transactions.GET("/:id/pdf", server.GenerateTransactionPDF) //TODO visak, stvari koje nisu u api spec

		transactions.POST("/payment", totp, server.PayoutMoneyToOtherAccount)
		transactions.POST("/transfer", totp, server.TransferMoneyBetweenAccounts)
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

	employees := api.Group("/employees", auth)
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

	accounts := api.Group("/accounts", auth)
	{
		accounts.POST("", server.CreateAccount)
		accounts.GET("", server.GetAccounts)
		accounts.GET("/:accountNumber", server.GetAccountByNumber)
		accounts.PATCH("/:accountNumber/name", server.UpdateAccountName)
		accounts.PATCH("/:accountNumber/limit", totp, server.UpdateAccountLimits)
	}

	loans := api.Group("/loans", auth)
	{
		loans.GET("", server.GetLoans)
		loans.GET("/:loanNumber", server.GetLoanByNumber)
	}

	loanRequests := api.Group("/loan-requests", auth)
	{
		loanRequests.POST("", server.CreateLoanRequest)
		loanRequests.GET("", server.GetLoanRequests)
		loanRequests.PATCH("/:id/approve", auth, secured("manage_contracts"), server.ApproveLoanRequest)
		loanRequests.PATCH("/:id/reject", server.RejectLoanRequest)
	}

	cards := api.Group("/cards")
	{
		cards.GET("", auth, server.GetCards)
		cards.POST("", auth, server.RequestCard)
		cards.GET("/confirm", server.ConfirmCard) //TODO visak, stvari koje nisu u api spec
		cards.PATCH("/:cardNumber/block", auth, server.BlockCard)
	}

	api.GET("/exchange-rates", auth, server.GetExchangeRates)

	exchange := api.Group("/exchange")
	{
		exchange.POST("/convert", server.ConvertMoney) //TODO visak, stvari koje nisu u api spec
	}
}

func (s *Server) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
