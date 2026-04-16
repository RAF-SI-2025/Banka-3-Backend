package gateway

import (
	"net/http"
	"os"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func setupCors(router *gin.Engine) {
	origin := os.Getenv("CORS_ORIGIN")
	if origin == "" {
		origin = "http://localhost:5173"
	}
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{origin},
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

	transactions := api.Group("/transactions", auth, secured("role:client"))
	{
		transactions.GET("", server.GetTransactions)
		transactions.GET("/:id", server.GetTransactionByID)
		transactions.GET("/:id/pdf", server.GenerateTransactionPDF)

		transactions.POST("/payment", totp, server.PayoutMoneyToOtherAccount)
		transactions.POST("/transfer", totp, server.TransferMoneyBetweenAccounts)
	}

	passwordReset := api.Group("/password-reset")
	{
		passwordReset.POST("/request", server.RequestPasswordReset)
		passwordReset.POST("/confirm", server.ConfirmPasswordReset)
	}

	api.GET("/clients/me", auth, secured("role:client"), server.GetMe) // van grupe
	clients := api.Group("/clients", auth, secured("manage_clients"))
	{
		clients.POST("", server.CreateClientAccount)
		clients.GET("", server.GetClients)
		clients.PUT("/:id", server.UpdateClient)
	}

	employees := api.Group("/employees", auth, secured("manage_employees"))
	{
		employees.POST("", server.CreateEmployeeAccount)
		employees.GET("/:employeeId", server.GetEmployeeByID)
		employees.DELETE("/:employeeId", server.DeleteEmployeeByID)
		employees.GET("", server.GetEmployees)
		employees.PATCH("/:employeeId", server.UpdateEmployee)
	}

	companies := api.Group("/companies", auth, secured("manage_companies"))
	{
		companies.POST("", server.CreateCompany)
		companies.GET("", server.GetCompanies)
		companies.GET("/:id", server.GetCompanyByID)
		companies.PUT("/:id", server.UpdateCompany)
	}

	accounts := api.Group("/accounts", auth)
	{
		accounts.POST("", secured("manage_accounts"), server.CreateAccount)
		accounts.GET("", secured("role:client|employee"), server.GetAccounts)
		accounts.GET("/:accountNumber", secured("role:client|employee"), server.GetAccountByNumber)
		accounts.PATCH("/:accountNumber/name", secured("role:client|employee"), server.UpdateAccountName)
		accounts.PATCH("/:accountNumber/limit", secured("manage_accounts"), totp, server.UpdateAccountLimits)
	}

	loans := api.Group("/loans", auth, secured("role:client|employee"))
	{
		loans.GET("", server.GetLoans)
		loans.GET("/:loanNumber", server.GetLoanByNumber)
	}

	loanRequests := api.Group("/loan-requests", auth)
	{
		loanRequests.POST("", secured("role:client"), server.CreateLoanRequest)
		loanRequests.GET("", secured("role:employee"), server.GetLoanRequests)
		loanRequests.PATCH("/:id/approve", secured("manage_loans"), server.ApproveLoanRequest)
		loanRequests.PATCH("/:id/reject", secured("manage_loans"), server.RejectLoanRequest)
	}

	cards := api.Group("/cards")
	{
		cards.GET("", auth, secured("role:client"), server.GetCards)
		cards.POST("", auth, secured("role:client"), totp, server.RequestCard)
		cards.GET("/confirm", server.ConfirmCard) // Ovo se poziva linkom iz mejla, NE DODAJEMO AUTH OVDE!!!
		cards.PATCH("/:cardNumber/block", auth, secured("role:client"), server.BlockCard)
	}

	api.GET("/exchange-rates", auth, secured("role:client"), server.GetExchangeRates)

	exchange := api.Group("/exchange")
	{
		exchange.POST("/convert", auth, secured("role:client"), server.ConvertMoney)
	}
}

func (s *Server) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
