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
		c.JSON(http.StatusOK, resp.BackupCodes)
	} else {
		c.JSON(http.StatusUnauthorized, gin.H{
			"message": "wrong code",
		})
	}
}

func (s *Server) CreateTransactionCode(c *gin.Context) {
	email := c.GetString("email")
	resp, err := s.TOTPClient.CreateTransactionCode(context.Background(), &userpb.CreateTransactionCodeRequest{
		Email: email,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusCreated, createTransactionCodeResponse{
		Code:           resp.Code,
		ValidUntilUnix: resp.ValidUntilUnix,
		MaxAttempts:    resp.MaxAttempts,
	})
}

func (s *Server) TOTPDisableBegin(c *gin.Context) {
	email := c.GetString("email")
	resp, err := s.TOTPClient.DisableBegin(context.Background(), &userpb.DisableBeginRequest{
		Email: email,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	if resp.Success {
		c.Status(http.StatusAccepted)
	} else {
		c.Status(http.StatusInternalServerError)
	}
}

func (s *Server) TOTPDisableConfirm(c *gin.Context) {
	var req totpDisableConfirmRequest
	if err := c.BindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	email := c.GetString("email")
	resp, err := s.TOTPClient.DisableConfirm(context.Background(), &userpb.DisableConfirmRequest{
		Email: email,
		Token: req.Token,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}
	if resp.Success {
		c.Status(http.StatusOK)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "bad token",
		})
	}
}
