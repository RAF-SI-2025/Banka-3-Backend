// Package logger configures the application's structured logger.
//
// Output is JSON on stdout by default (k8s-friendly), with the level taken
// from LOG_LEVEL and the format from LOG_FORMAT (json|text). The logger is
// an [*slog.Logger]; pass it through context with [Inject] / [From].
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey struct{}

// New returns an [*slog.Logger] honoring LOG_LEVEL and LOG_FORMAT env vars.
// Service is added as a default attribute on every record.
func New(service string) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level, AddSource: false}

	var handler slog.Handler
	if strings.ToLower(os.Getenv("LOG_FORMAT")) == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler).With("service", service)
}

// Inject stores logger in ctx for downstream retrieval.
func Inject(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, log)
}

// From returns the logger from ctx, or the default if none is present.
func From(ctx context.Context) *slog.Logger {
	if log, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return log
	}
	return slog.Default()
}
