package gateway

import (
	"context"
	"net/http"
	"strconv"
	"time"

	notificationpb "banka-raf/gen/notification"
	userpb "banka-raf/gen/user"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type validateTokenRequest struct {
	Token string `json:"token"`
}

type activationEmailRequest struct {
	ToAddr string `json:"to_addr"`
	Link   string `json:"link"`
}

type confirmationEmailRequest struct {
	ToAddr  string `json:"to_addr"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func SetupApi(router *gin.Engine, server *Server) {
	router.GET("/healthz", server.Healthz)

	auth := router.Group("/auth")
	{
		auth.POST("/login", server.Login)
		auth.POST("/refresh", server.Refresh)
		auth.POST("/validate/access", server.ValidateAccessToken)
		auth.POST("/validate/refresh", server.ValidateRefreshToken)
	}

	router.GET("/employees/:id", server.GetEmployeeByID)

	emails := router.Group("/emails")
	{
		emails.POST("/activation", server.SendActivationEmail)
		emails.POST("/confirmation", server.SendConfirmationEmail)
	}
}

func (s *Server) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid json body")
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
		c.String(http.StatusBadRequest, "invalid json body")
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

func (s *Server) ValidateAccessToken(c *gin.Context) {
	var req validateTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid json body")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.ValidateAccessToken(ctx, &userpb.ValidateTokenRequest{
		Token: req.Token,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"valid": resp.Valid})
}

func (s *Server) ValidateRefreshToken(c *gin.Context) {
	var req validateTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid json body")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.ValidateRefreshToken(ctx, &userpb.ValidateTokenRequest{
		Token: req.Token,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"valid": resp.Valid})
}

func (s *Server) GetEmployeeByID(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "employee id must be a valid integer")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.GetEmployeeById(ctx, &userpb.GetEmployeeByIdRequest{
		Id: id,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":         resp.Id,
		"first_name": resp.FirstName,
		"last_name":  resp.LastName,
		"email":      resp.Email,
		"position":   resp.Position,
		"active":     resp.Active,
	})
}

func (s *Server) SendActivationEmail(c *gin.Context) {
	var req activationEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid json body")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	resp, err := s.NotificationClient.SendActivationEmail(ctx, &notificationpb.ActivationMailRequest{
		ToAddr: req.ToAddr,
		Link:   req.Link,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"successful": resp.Successful})
}

func (s *Server) SendConfirmationEmail(c *gin.Context) {
	var req confirmationEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "invalid json body")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	resp, err := s.NotificationClient.SendConfirmationEmail(ctx, &notificationpb.ConfirmationMailRequest{
		ToAddr:  req.ToAddr,
		Subject: req.Subject,
		Body:    req.Body,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"successful": resp.Successful})
}

func writeGRPCError(c *gin.Context, err error) {
	st, ok := status.FromError(err)
	if !ok {
		c.String(http.StatusInternalServerError, "internal server error")
		return
	}

	switch st.Code() {
	case codes.InvalidArgument:
		c.String(http.StatusBadRequest, st.Message())
	case codes.AlreadyExists:
		c.String(http.StatusConflict, st.Message())
	case codes.NotFound:
		c.String(http.StatusNotFound, st.Message())
	case codes.Unauthenticated:
		c.String(http.StatusUnauthorized, st.Message())
	case codes.PermissionDenied:
		c.String(http.StatusForbidden, st.Message())
	default:
		c.String(http.StatusInternalServerError, st.Message())
	}
}
