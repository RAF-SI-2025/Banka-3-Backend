// Command user is the entrypoint for the user service.
//
// Boots a gRPC server (auth, employees, clients, permissions) and a
// Kubernetes probe HTTP server. All configuration is read from the
// environment.
package main

import (
	"fmt"
	"os"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "user: %v\n", err)
		os.Exit(1)
	}
}
