package gateway

import (
	"context"
	"net/http"
	"time"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"github.com/gin-gonic/gin"
)

func actuaryToJSON(e *userpb.GetEmployeeResponse) gin.H {
	perms := e.Permissions
	if perms == nil {
		perms = []string{}
	}
	return gin.H{
		"id":            e.Id,
		"first_name":    e.FirstName,
		"last_name":     e.LastName,
		"email":         e.Email,
		"position":      e.Position,
		"phone":         e.PhoneNumber,
		"active":        e.Active,
		"permissions":   perms,
		"limit":         e.Limit,
		"used_limit":    e.UsedLimit,
		"need_approval": e.NeedApproval,
	}
}

func (s *Server) GetActuaries(c *gin.Context) {
	var query getEmployeesQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.GetActuaries(ctx, &userpb.GetEmployeesRequest{
		FirstName: query.FirstName,
		LastName:  query.LastName,
		Email:     query.Email,
		Position:  query.Position,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	out := make([]gin.H, 0, len(resp.Employees))
	for _, e := range resp.Employees {
		out = append(out, gin.H{
			"id":            e.Id,
			"first_name":    e.FirstName,
			"last_name":     e.LastName,
			"email":         e.Email,
			"position":      e.Position,
			"phone":         e.PhoneNumber,
			"active":        e.Active,
			"limit":         e.Limit,
			"used_limit":    e.UsedLimit,
			"need_approval": e.NeedApproval,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) SetActuaryLimit(c *gin.Context) {
	var uri actuaryByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "actuary id is required and must be a valid integer")
		return
	}
	var body updateActuaryLimitRequest
	if err := c.BindJSON(&body); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.UpdateEmployeeTradingLimit(ctx, &userpb.UpdateEmployeeTradingLimitRequest{
		Id:          uri.ActuaryID,
		Limit:       body.Limit,
		CallerEmail: c.GetString("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, actuaryToJSON(resp))
}

func (s *Server) ResetActuaryUsedLimit(c *gin.Context) {
	var uri actuaryByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "actuary id is required and must be a valid integer")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	zero := int64(0)
	resp, err := s.UserClient.UpdateEmployeeTradingLimit(ctx, &userpb.UpdateEmployeeTradingLimitRequest{
		Id:          uri.ActuaryID,
		UsedLimit:   &zero,
		CallerEmail: c.GetString("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, actuaryToJSON(resp))
}

func (s *Server) SetActuaryNeedApproval(c *gin.Context) {
	var uri actuaryByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "actuary id is required and must be a valid integer")
		return
	}
	var body updateActuaryNeedApprovalRequest
	if err := c.BindJSON(&body); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.UpdateEmployeeNeedApproval(ctx, &userpb.UpdateEmployeeNeedApprovalRequest{
		Id:           uri.ActuaryID,
		NeedApproval: *body.NeedApproval,
		CallerEmail:  c.GetString("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, actuaryToJSON(resp))
}
