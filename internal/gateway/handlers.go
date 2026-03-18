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
		DateOfBirth: req.DateOfBirth,
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
		DateOfBirth: req.DateOfBirth,
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

func permissionNamesFromProtoPerm(perm *userpb.Permissions) []string {
	if perm == nil || perm.Permision == "" {
		return []string{}
	}

	return []string{perm.Permision}
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

	permissions := permissionNamesFromProtoPerm(resp.Perms)

	c.JSON(http.StatusOK, gin.H{
		"id":            resp.Id,
		"first_name":    resp.FirstName,
		"last_name":     resp.LastName,
		"date_of_birth": resp.DateOfBirth,
		"gender":        resp.Gender,
		"email":         resp.Email,
		"phone_number":  resp.PhoneNumber,
		"address":       resp.Address,
		"username":      resp.Username,
		"position":      resp.Position,
		"department":    resp.Department,
		"active":        resp.Active,
		"permissions":   permissions,
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

	employees := make([]gin.H, 0, len(resp.Employees))
	for _, emp := range resp.Employees {
		employees = append(employees, gin.H{
			"id":           emp.Id,
			"first_name":   emp.FirstName,
			"last_name":    emp.LastName,
			"email":        emp.Email,
			"position":     emp.Position,
			"phone_number": emp.PhoneNumber,
			"active":       emp.Active,
			"permissions":  permissionNamesFromProtoPerm(emp.Perms),
		})
	}

	c.JSON(http.StatusOK, employees)
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

	getCtx, getCancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer getCancel()

	current, err := s.UserClient.GetEmployeeById(getCtx, &userpb.GetEmployeeByIdRequest{
		Id: uri.EmployeeID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	lastName := current.LastName
	if req.LastName != nil {
		lastName = *req.LastName
	}

	gender := current.Gender
	if req.Gender != nil {
		gender = *req.Gender
	}

	phoneNumber := current.PhoneNumber
	if req.PhoneNumber != nil {
		phoneNumber = *req.PhoneNumber
	}

	address := current.Address
	if req.Address != nil {
		address = *req.Address
	}

	position := current.Position
	if req.Position != nil {
		position = *req.Position
	}

	department := current.Department
	if req.Department != nil {
		department = *req.Department
	}

	active := current.Active
	if req.Active != nil {
		active = *req.Active
	}

	perms := []*userpb.Permissions{}
	if current.Perms != nil && current.Perms.Permision != "" {
		perms = append(perms, &userpb.Permissions{
			Id:        current.Perms.Id,
			Permision: current.Perms.Permision,
		})
	}

	if req.Permissions != nil {
		requested := *req.Permissions

		if len(requested) == 0 {
			perms = []*userpb.Permissions{}
		} else if current.Perms != nil && len(requested) == 1 && requested[0] == current.Perms.Permision {
			perms = []*userpb.Permissions{
				{
					Id:        current.Perms.Id,
					Permision: current.Perms.Permision,
				},
			}
		} else {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "updating permissions by name is not supported yet",
			})
			return
		}
	}

	updateCtx, updateCancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer updateCancel()

	updateResp, err := s.UserClient.UpdateEmployee(updateCtx, &userpb.UpdateEmployeeRequest{
		Id:          uri.EmployeeID,
		LastName:    lastName,
		Gender:      gender,
		PhoneNumber: phoneNumber,
		Address:     address,
		Position:    position,
		Department:  department,
		Active:      active,
		Perms:       perms,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	if !updateResp.Valid {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"valid":    false,
			"response": updateResp.Response,
		})
		return
	}

	finalCtx, finalCancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer finalCancel()

	updated, err := s.UserClient.GetEmployeeById(finalCtx, &userpb.GetEmployeeByIdRequest{
		Id: uri.EmployeeID,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	permissions := permissionNamesFromProtoPerm(updated.Perms)

	c.JSON(http.StatusOK, gin.H{
		"id":            updated.Id,
		"first_name":    updated.FirstName,
		"last_name":     updated.LastName,
		"date_of_birth": updated.DateOfBirth,
		"gender":        updated.Gender,
		"email":         updated.Email,
		"phone_number":  updated.PhoneNumber,
		"address":       updated.Address,
		"username":      updated.Username,
		"position":      updated.Position,
		"department":    updated.Department,
		"active":        updated.Active,
		"permissions":   permissions,
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
