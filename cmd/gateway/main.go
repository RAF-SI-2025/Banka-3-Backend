package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/RAF-SI-2025/Banka-3-Backend/internal/gateway"
)

func main() {
	router := gin.Default()

	server, err := gateway.NewServer()
	if err != nil {
		log.Fatalf("Error connecting to services: %v", err)
	}

	gateway.SetupApi(router, server)

	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "8080"
	}

	if err := router.Run(":" + httpPort); err != nil {
		log.Fatalf("gateway stopped: %v", err)
	}
}
