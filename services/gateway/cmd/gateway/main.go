// Command gateway is the entrypoint for the public HTTP gateway.
//
// Translates REST → gRPC via grpc-gateway, terminates JWT auth, and
// exposes inter-bank endpoints used in celina 5.
package main

import (
	"fmt"
	"os"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "gateway: %v\n", err)
		os.Exit(1)
	}
}
