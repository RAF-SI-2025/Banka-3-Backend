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
		c.Set("permissions", resp.Permissions)
		c.Set("role", resp.Role)
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

func PermissionMiddleware() func(...string) gin.HandlerFunc {
	return func(required ...string) gin.HandlerFunc {
		return func(c *gin.Context) {
			permsVal, permsExists := c.Get("permissions")
			roleVal, roleExists := c.Get("role")

			userPerms, _ := permsVal.([]string)
			userRole, _ := roleVal.(string)

			// Admin permission bypasses all checks
			if slices.Contains(userPerms, "admin") {
				c.Next()
				return
			}

			for _, req := range required {
				if strings.HasPrefix(req, "role:") {
					// Role check: "role:client", "role:employee", "role:client|employee"
					if !roleExists || userRole == "" {
						c.AbortWithStatus(403)
						return
					}
					allowedRoles := strings.Split(req[5:], "|")
					if !slices.Contains(allowedRoles, userRole) {
						c.AbortWithStatus(403)
						return
					}
				} else {
					// Permission check
					if !permsExists {
						c.AbortWithStatus(403)
						return
					}
					if !slices.Contains(userPerms, req) {
						c.AbortWithStatus(403)
						return
					}
				}
			}
			c.Next()
		}
	}
}
