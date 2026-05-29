package app

import (
	"context"
	"log/slog"
	"time"
)

// runJobLoop fires fn at every interval, until ctx is cancelled. Errors
// are logged but not surfaced — a transient DB blip shouldn't take the
// whole service down. The first tick is offset by interval (so we
// don't run an installment job at boot before any traffic arrives).
func runJobLoop(ctx context.Context, log *slog.Logger, name string, interval time.Duration, fn func(context.Context) error) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := fn(ctx); err != nil {
				log.Warn("scheduled job failed", "job", name, "error", err)
			} else {
				log.Info("scheduled job ran", "job", name)
			}
		}
	}
}
