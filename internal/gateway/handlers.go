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

	auth := AuthenticatedMiddleware(server.UserClient)
	secured := PermissionMiddleware(server.UserClient)
	totp := TOTPMiddleware(server.TOTPClient)

	{
		api.POST("/login", server.Login)
		api.POST("/logout", auth, server.Logout)
		api.POST("/token/refresh", server.Refresh)
		api.POST("/totp/setup/begin", auth, server.TOTPSetupBegin)
		api.POST("/totp/setup/confirm", auth, server.TOTPSetupConfirm)
		api.POST("/totp/disable/begin", auth, server.TOTPDisableBegin)
		api.POST("/totp/disable/confirm", auth, server.TOTPDisableConfirm)
	}

	recipients := api.Group("/recipients", auth, secured("role:client"))
	{
		recipients.GET("", server.GetPaymentRecipients)
		recipients.POST("", server.CreatePaymentRecipient)
		recipients.PUT("/:id", server.UpdatePaymentRecipient)
		recipients.DELETE("/:id", server.DeletePaymentRecipient)
	}

	transactions := api.Group("/transactions", auth)
	{
		transactions.GET("", server.GetTransactions)
		transactions.GET("/:id", server.GetTransactionByID)
		transactions.GET("/:id/pdf", server.GenerateTransactionPDF)

		transactions.POST("/payment", secured("role:client"), totp, server.PayoutMoneyToOtherAccount)
		transactions.POST("/transfer", secured("role:client"), totp, server.TransferMoneyBetweenAccounts)
	}

	passwordReset := api.Group("/password-reset")
	{
		passwordReset.POST("/request", server.RequestPasswordReset)
		passwordReset.POST("/confirm", server.ConfirmPasswordReset)
	}

	clients := api.Group("/clients", auth)
	{
		clients.POST("", secured("role:employee"), server.CreateClientAccount)
		clients.GET("", secured("role:employee"), server.GetClients)
		clients.PUT("/:id", server.UpdateClient)
	}

	employees := api.Group("/employees", auth, secured("role:employee"))
	{
		employees.POST("", server.CreateEmployeeAccount)
		employees.GET("/:employeeId", server.GetEmployeeByID)
		employees.DELETE("/:employeeId", server.DeleteEmployeeByID)
		employees.GET("", server.GetEmployees)
		employees.PATCH("/:employeeId", server.UpdateEmployee)
	}

	companies := api.Group("/companies", auth)
	{
		companies.POST("", server.CreateCompany)
		companies.GET("", server.GetCompanies)
		companies.GET("/:id", server.GetCompanyByID)
		companies.PUT("/:id", server.UpdateCompany)
	}

	accounts := api.Group("/accounts", auth)
	{
		accounts.POST("", secured("role:employee"), server.CreateAccount)
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
		loanRequests.POST("", secured("role:client"), server.CreateLoanRequest)
		loanRequests.GET("", server.GetLoanRequests)
		loanRequests.PATCH("/:id/approve", secured("role:employee", "manage_contracts"), server.ApproveLoanRequest)
		loanRequests.PATCH("/:id/reject", secured("role:employee", "manage_contracts"), server.RejectLoanRequest)
	}

	cards := api.Group("/cards")
	{
		cards.GET("", auth, server.GetCards)
		cards.POST("", auth, server.RequestCard)
		cards.GET("/confirm", server.ConfirmCard)
		cards.PATCH("/:cardNumber/block", auth, server.BlockCard)
	}

	api.GET("/exchange-rates", auth, server.GetExchangeRates)

	exchange := api.Group("/exchange")
	{
		exchange.POST("/convert", auth, server.ConvertMoney)
	}
}

func (s *Server) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
