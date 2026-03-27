package gateway

import (
	"context"
	"net/http"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"github.com/gin-gonic/gin"
)

func (s *Server) TOTPSetupBegin(c *gin.Context) {
	email := c.GetString("email")
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
		writeBindError(c, err)
		return
	}
	email := c.GetString("email")
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

func (s *Server) TOTPStatus(c *gin.Context) {
	email := c.GetString("email")
	resp, err := s.TOTPClient.TOTPStatus(context.Background(), &userpb.TOTPStatusRequest{
		Email: email,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"active": resp.Active,
	})
}
