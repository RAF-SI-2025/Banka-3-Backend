package gateway

import (
	userpb "banka-raf/gen/user"
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Placeholder for future middleware (auth, logging, prometheus, etc.).
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

		if !resp.Valid {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		t, _, err := new(jwt.Parser).ParseUnverified(token, jwt.MapClaims{})
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		if claims, ok := t.Claims.(jwt.MapClaims); ok {
			email, err := claims.GetSubject()
			if err != nil {
				c.AbortWithStatus(http.StatusUnauthorized)
			}
			c.Set("email", email)
			c.Next()
		}
	}
}
