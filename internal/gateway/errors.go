package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	TotpAleadyEnabledCode = 20
)

func writeBindError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{
		"error":   "invalid request body",
		"details": err.Error(),
	})
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
		c.JSON(http.StatusUnauthorized, gin.H{
			"message": st.Message(),
		})
	case codes.PermissionDenied:
		c.String(http.StatusForbidden, st.Message())
	case codes.FailedPrecondition:
		c.String(http.StatusConflict, st.Message())
	case TotpAleadyEnabledCode:
		c.String(http.StatusConflict, st.Message())
	default:
		c.String(http.StatusInternalServerError, st.Message())
	}
}
