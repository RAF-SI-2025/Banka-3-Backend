package gateway

import (
	"context"
	"net/http"
	"time"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"github.com/gin-gonic/gin"
)

func clientResponse(client *userpb.Client) gin.H {
	return gin.H{
		"id":            client.Id,
		"first_name":    client.FirstName,
		"last_name":     client.LastName,
		"date_of_birth": client.DateOfBirth,
		"gender":        client.Gender,
		"email":         client.Email,
		"phone_number":  client.PhoneNumber,
		"address":       client.Address,
	}
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

func (s *Server) GetClients(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.GetClients(ctx, &userpb.GetClientsRequest{
		FirstName: c.Query("first_name"),
		LastName:  c.Query("last_name"),
		Email:     c.Query("email"),
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	clients := make([]gin.H, 0, len(resp.Clients))
	for _, client := range resp.Clients {
		clients = append(clients, clientResponse(client))
	}

	c.JSON(http.StatusOK, clients)
}

func (s *Server) UpdateClient(c *gin.Context) {
	var uri clientByIDURI
	if err := c.ShouldBindUri(&uri); err != nil {
		c.String(http.StatusBadRequest, "client id is required and must be a valid integer")
		return
	}

	var req updateClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	resp, err := s.UserClient.UpdateClient(ctx, &userpb.UpdateClientRequest{
		Id:          uri.ClientID,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		Gender:      req.Gender,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		Address:     req.Address,
	})
	if err != nil {
		writeGRPCError(c, err)
		return
	}

	if !resp.Valid {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": resp.Response})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":            uri.ClientID,
		"first_name":    req.FirstName,
		"last_name":     req.LastName,
		"date_of_birth": req.DateOfBirth,
		"gender":        req.Gender,
		"email":         req.Email,
		"phone_number":  req.PhoneNumber,
		"address":       req.Address,
	})
}
