package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

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

	passwordReset := api.Group("/password-reset")
	{
		passwordReset.POST("/request", server.RequestPasswordReset)
		passwordReset.POST("/confirm", server.ConfirmPasswordReset)
	}

	clients := api.Group("/clients")
	{
		clients.POST("", server.CreateClientAccount)
	}

	employees := api.Group("/employees")
	{
		employees.POST("", server.CreateEmployeeAccount)
		employees.GET("/:id", server.GetEmployeeByID)
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

func companyResponse(company *userpb.Company) gin.H {
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

	resp, err := s.UserClient.CreateCompany(ctx, &userpb.CreateCompanyRequest{
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

	resp, err := s.UserClient.GetCompanyById(ctx, &userpb.GetCompanyByIdRequest{
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

	resp, err := s.UserClient.GetCompanies(ctx, &userpb.GetCompaniesRequest{})
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

	resp, err := s.UserClient.UpdateCompany(ctx, &userpb.UpdateCompanyRequest{
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
		"id":            resp.Id,
		"first_name":    resp.FirstName,
		"last_name":     resp.LastName,
		"birth_date":    time.Unix(resp.BirthDate, 0).Format(time.DateOnly),
		"gender":        resp.Gender,
		"email":         resp.Email,
		"phone_numbere": resp.PhoneNumber,
		"address":       resp.PhoneNumber,
		"username":      resp.Username,
		"position":      resp.Position,
		"department":    resp.Department,
		"active":        resp.Active,
		"permissions":   resp.Permissions,
	})
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
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	resp, err := s.UserClient.UpdateEmployee(ctx, &userpb.UpdateEmployeeRequest{
		Id:          uri.EmployeeID,
		LastName:    *req.LastName,
		Gender:      *req.Gender,
		PhoneNumber: *req.PhoneNumber,
		Address:     *req.Address,
		Position:    *req.Position,
		Department:  *req.Department,
		Active:      *req.Active,
		Permissions: *req.Permissions,
	})
	if err != nil {
		writeGRPCError(c, err)
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) GetAllEmployees(c *gin.Context) {
	var uri getEmployeesURI
	if err := c.Bind(&uri); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.GetEmployees(ctx, &userpb.GetEmployeesRequest{
		FirstName: uri.FirstName,
		LastName:  uri.LastName,
		Email:     uri.Email,
		Position:  uri.Position,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	if resp.Employees != nil {
		c.JSON(http.StatusOK, resp.Employees)
	} else {
		dummy := make([]string, 0)
		c.JSON(http.StatusOK, dummy)
	}

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
