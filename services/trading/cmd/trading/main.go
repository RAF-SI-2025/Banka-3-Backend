// Command trading is the entrypoint for the trading service.
//
// Boots a gRPC server (auth, employees, clients, permissions) and a
// Kubernetes probe HTTP server. All configuration is read from the
// environment.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		// app.Run installs the JSON logger as slog's default before any
		// fallible init, so this lands as a structured record; the stderr
		// line stays for bare `docker logs` ergonomics.
		slog.Error("trading service exited", "err", err)
		fmt.Fprintf(os.Stderr, "trading: %v\n", err)
		os.Exit(1)
	}
}
