package logger

import (
	"time"

	"github.com/gin-gonic/gin"
)

const RequestIDHeader = "X-Request-ID"

// GinMiddleware logs every HTTP request and stashes a request-scoped logger
// in the request context. Generates an X-Request-ID if the client didn't send one.
func GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		reqID := c.GetHeader(RequestIDHeader)
		if reqID == "" {
			reqID = newRequestID()
		}
		c.Writer.Header().Set(RequestIDHeader, reqID)

		l := L().With(
			"request_id", reqID,
			"http_method", c.Request.Method,
			"http_path", c.FullPath(),
		)

		ctx := WithContext(c.Request.Context(), l)
		ctx = WithRequestID(ctx, reqID)
		c.Request = c.Request.WithContext(ctx)

		l.DebugContext(ctx, "http start")
		c.Next()
		dur := time.Since(start)

		status := c.Writer.Status()
		attrs := []any{
			"status", status,
			"duration_ms", dur.Milliseconds(),
			"client_ip", c.ClientIP(),
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "err", c.Errors.String())
		}
		switch {
		case status >= 500:
			l.ErrorContext(ctx, "http end", attrs...)
		case status >= 400:
			l.WarnContext(ctx, "http end", attrs...)
		default:
			l.InfoContext(ctx, "http end", attrs...)
		}
	}
}
