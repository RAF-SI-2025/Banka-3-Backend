// Command gateway is the entrypoint for the public HTTP gateway.
//
// Translates REST → gRPC via grpc-gateway, terminates JWT auth, and
// exposes inter-bank endpoints used in celina 5.
package main

import (
	"os"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		// The error is wrapped with the failing dependency by app.Run
		// ("otelinit: …", "dial upstreams: …", "redis: …", "mount
		// router: …"), so one structured line names the culprit.
		logger.New("gateway").Error("gateway exited", "err", err)
		os.Exit(1)
	}
}
