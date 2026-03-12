package main

import (
	"banka-raf/internal/gateway"
	"log"

	"github.com/gin-gonic/gin"
)

func main() {
	router := gin.Default()

	server, err := gateway.NewServer()
	if err != nil {
		log.Fatalf("Error connecting to services: %v", err)
	}

	gateway.SetupApi(router, server)

	if err := router.Run(":8080"); err != nil {
		log.Fatalf("gateway stopped: %v", err)
	}
}
