// Command exchange is the entrypoint for the exchange service.
//
// Boots a gRPC server (auth, employees, clients, permissions) and a
// Kubernetes probe HTTP server. All configuration is read from the
// environment.
package main

import (
	"fmt"
	"os"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "exchange: %v\n", err)
		os.Exit(1)
	}
}
