// Package shutdown wires SIGINT / SIGTERM into a context that cancels on
// the first signal. A second signal in the same process forces immediate
// exit.
package shutdown

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// Context returns a context cancelled on the first SIGINT or SIGTERM. The
// returned cancel must be called by the caller (use defer cancel()).
func Context() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
