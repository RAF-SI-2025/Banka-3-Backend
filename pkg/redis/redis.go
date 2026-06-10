// Package redis wraps a [redis.Client] for service use.
package redis

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

// Open returns a connected redis client. Caller closes via client.Close().
func Open(ctx context.Context, addr, password string) (*redis.Client, error) {
	c := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})
	// redisotel hooks into go-redis's per-command callback so every
	// SET/GET/HGET/EXPIRE/... becomes a child span of the calling
	// request. Failure to install the hook is non-fatal — log only,
	// keep serving; redis still works, just without trace correlation.
	if err := redisotel.InstrumentTracing(c); err != nil {
		slog.WarnContext(ctx, "redis otel instrumentation failed", "err", err, "addr", addr)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx).Err(); err != nil {
		slog.ErrorContext(ctx, "redis ping failed", "err", err, "addr", addr)
		if cerr := c.Close(); cerr != nil {
			slog.WarnContext(ctx, "redis close after failed ping failed", "err", cerr, "addr", addr)
		}
		return nil, fmt.Errorf("ping: %w", err)
	}
	return c, nil
}

// Ping returns nil if the client can reach the server.
func Ping(ctx context.Context, c *redis.Client) error {
	return c.Ping(ctx).Err()
}
