// Command bank is the entrypoint for the bank service.
//
// Boots a gRPC server (auth, employees, clients, permissions) and a
// Kubernetes probe HTTP server. All configuration is read from the
// environment.
package main

import (
	"fmt"
	"os"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		// Structured record first (k8s log pipelines key off these),
		// stderr line second so the failure is visible even when stdout
		// is redirected or the JSON pipeline is misconfigured.
		logger.New("bank").Error("bank service exited", "err", err)
		fmt.Fprintf(os.Stderr, "bank: %v\n", err)
		os.Exit(1)
	}
}
