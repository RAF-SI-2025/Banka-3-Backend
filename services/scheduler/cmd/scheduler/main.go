// Command scheduler is the entrypoint for the scheduler service — the
// single non-replicatable driver of every cluster-wide cron + worker.
// All configuration is read from the environment.
package main

import (
	"fmt"
	"os"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/scheduler/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "scheduler: %v\n", err)
		os.Exit(1)
	}
}
