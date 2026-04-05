package gateway

import (
	"context"
	"net/http"
	"time"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"github.com/gin-gonic/gin"
)

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

	permissions := resp.Permissions
	if permissions == nil {
		permissions = []string{}
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken":  resp.AccessToken,
		"refreshToken": resp.RefreshToken,
		"permissions":  permissions,
	})
}

func (s *Server) Logout(c *gin.Context) {
	email := c.GetString("email")

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

	permissions := resp.Permissions
	if permissions == nil {
		permissions = []string{}
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  resp.AccessToken,
		"refresh_token": resp.RefreshToken,
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

func (s *Server) getAuthenticatedClientID(c *gin.Context) (int64, bool) {
	role := c.GetString("role")
	if role != "client" {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "authenticated user is not a client",
		})
		return 0, false
	}

	email := c.GetString("email")

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
