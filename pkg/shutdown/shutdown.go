// Package shutdown wires SIGINT / SIGTERM into a context that cancels on
// the first signal. A second signal in the same process forces immediate
// exit.
package shutdown

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Context returns a context cancelled on the first SIGINT or SIGTERM. The
// returned cancel must be called by the caller (use defer cancel()).
func Context() (context.Context, context.CancelFunc) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	// Side channel purely for logging which signal arrived; the cancel
	// semantics above are untouched. Both registrations receive the
	// signal, so this never steals it from NotifyContext.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer signal.Stop(sigCh)
		select {
		case sig := <-sigCh:
			slog.Info("shutdown signal received", "signal", sig.String())
		case <-ctx.Done():
			// Cancelled — drain a signal that raced the cancellation so
			// it is still logged, then stop listening.
			select {
			case sig := <-sigCh:
				slog.Info("shutdown signal received", "signal", sig.String())
			default:
			}
		}
	}()

	return ctx, cancel
}
