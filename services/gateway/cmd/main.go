package main

import (
	"os"

	"github.com/gin-gonic/gin"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/observability"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/gateway"
)

func main() {
	logger.Init("gateway")
	stopMetrics := observability.StartMetricsServer("gateway", os.Getenv("METRICS_PORT"))
	defer stopMetrics()

	router := gin.New()
	router.Use(gin.Recovery(), logger.GinMiddleware(), observability.GinMiddleware("gateway"))

	server, err := gateway.NewServer()
	if err != nil {
		logger.L().Error("error connecting to services", "err", err)
		os.Exit(1)
	}

	gateway.SetupApi(router, server)

	if err := router.Run(":8080"); err != nil {
		logger.L().Error("gateway stopped", "err", err)
		os.Exit(1)
	}
}
