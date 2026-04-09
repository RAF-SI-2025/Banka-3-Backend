package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lmittmann/tint"
)

var (
	levelVar   = new(slog.LevelVar)
	initOnce   sync.Once
	rootLogger *slog.Logger
)

type ctxKey struct{}

// Init sets up the global logger. Call once from main.
func Init(service string) *slog.Logger {
	initOnce.Do(func() {
		levelVar.Set(parseLevel(os.Getenv("LOG_LEVEL")))

		opts := &slog.HandlerOptions{
			Level:     levelVar,
			AddSource: true,
		}

		var h slog.Handler
		if strings.EqualFold(os.Getenv("LOG_FORMAT"), "text") {
			h = tint.NewHandler(os.Stdout, &tint.Options{
				Level:      levelVar,
				AddSource:  true,
				TimeFormat: time.Kitchen,
			})
		} else {
			h = slog.NewJSONHandler(os.Stdout, opts)
		}

		rootLogger = slog.New(h).With("service", service)
		slog.SetDefault(rootLogger)
	})
	return rootLogger
}

func SetLevel(lvl string) {
	levelVar.Set(parseLevel(lvl))
}

func L() *slog.Logger {
	if rootLogger == nil {
		return slog.Default()
	}
	return rootLogger
}

func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return L()
	}
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return L()
}

func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
