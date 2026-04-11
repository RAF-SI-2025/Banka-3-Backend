package gateway

import (
	"context"
	"net/http"
	"time"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"github.com/gin-gonic/gin"
)

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
