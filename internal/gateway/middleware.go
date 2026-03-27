package gateway

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
)

// NoopMiddleware Placeholder for future middleware (auth, logging, prometheus, etc.).
func NoopMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}

func AuthenticatedMiddleware(user userpb.UserServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" || !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		token := header[7:]
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		resp, err := user.ValidateAccessToken(ctx, &userpb.ValidateTokenRequest{
			Token: token,
		})
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		c.Set("email", resp.Sub)
		c.Set("exp", resp.Exp)
		c.Set("iat", resp.Iat)
		c.Next()
	}
}

func TOTPMiddleware(totp userpb.TOTPServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		key, keyPresent := c.Get("email")
		if !keyPresent || key == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		email, ok := key.(string)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		header := c.GetHeader("TOTP")
		if header == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		resp, err := totp.VerifyCode(context.Background(), &userpb.VerifyCodeRequest{
			Email: email,
			Code:  header,
		})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "user doesn't have TOTP setup"})
			return
		}
		if !resp.Valid {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
}

func PermissionMiddleware(user userpb.UserServiceClient) func(...string) gin.HandlerFunc {
	return func(permissions ...string) gin.HandlerFunc {
		return func(c *gin.Context) {
			email, exists := c.Get("email")
			if !exists {
				c.AbortWithStatus(401)
				return
			}

			emp, err := user.GetEmployeeByEmail(c, &userpb.GetEmployeeByEmailRequest{
				Email: email.(string),
			})
			if err != nil {
				c.AbortWithStatus(403)
			}
			for _, perm := range permissions {
				if !slices.Contains(emp.Permissions, perm) {
					c.AbortWithStatus(403)
				}
			}
			c.Next()
		}
	}
}
