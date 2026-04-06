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

// AuthenticatedMiddleware validates the JWT and checks that an active session exists.
// It only sets identity fields (email, exp, iat) in context.
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

// PermissionMiddleware fetches the user's role and permissions from the session store
// and checks them against the required values.
// Prefix "role:" checks the role (e.g. "role:client", "role:client|employee").
// Plain strings check permissions (e.g. "manage_contracts").
// The "admin" permission bypasses all checks.
func PermissionMiddleware(user userpb.UserServiceClient) func(...string) gin.HandlerFunc {
	return func(required ...string) gin.HandlerFunc {
		return func(c *gin.Context) {
			email := c.GetString("email")
			if email == "" {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}

			// Fetch role/permissions from Redis via gRPC (once per request)
			var userRole string
			var userPerms []string
			if cached, exists := c.Get("role"); exists {
				userRole, _ = cached.(string)
				permsVal, _ := c.Get("permissions")
				userPerms, _ = permsVal.([]string)
			} else {
				ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
				defer cancel()
				resp, err := user.GetUserPermissions(ctx, &userpb.GetUserPermissionsRequest{
					Email: email,
				})
				if err != nil {
					c.AbortWithStatus(http.StatusForbidden)
					return
				}
				userRole = resp.Role
				userPerms = resp.Permissions
				c.Set("role", userRole)
				c.Set("permissions", userPerms)
			}

			// Admin permission bypasses all checks
			if slices.Contains(userPerms, "admin") {
				c.Next()
				return
			}

			for _, req := range required {
				if strings.HasPrefix(req, "role:") {
					// Role check: "role:client", "role:employee", "role:client|employee"
					allowedRoles := strings.Split(req[5:], "|")
					if !slices.Contains(allowedRoles, userRole) {
						c.AbortWithStatus(http.StatusForbidden)
						return
					}
				} else {
					// Permission check
					if !slices.Contains(userPerms, req) {
						c.AbortWithStatus(http.StatusForbidden)
						return
					}
				}
			}
			c.Next()
		}
	}
}
