// Command notification is the entrypoint for the notification service.
//
// Boots a gRPC server (auth, employees, clients, permissions) and a
// Kubernetes probe HTTP server. All configuration is read from the
// environment.
package main

import (
	"fmt"
	"os"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/notification/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "notification: %v\n", err)
		os.Exit(1)
	}
}
